-- Transactions and postings, and the constraint that keeps them balanced.

CREATE TABLE transactions (
    id          uuid PRIMARY KEY,
    -- The civil date the transaction belongs to. The only field any balance
    -- calculation reads when deciding when something happened.
    txn_date    date NOT NULL,
    -- Display only. A report must say the same thing to every reader in every
    -- zone, so nothing computed ever consults these two.
    occurred_at timestamptz,
    timezone    text NOT NULL DEFAULT '',
    payee       text NOT NULL DEFAULT '',
    memo        text NOT NULL DEFAULT '',

    -- Section 5.3: postings are immutable, so a correction and a deletion are
    -- both new transactions that reverse an old one. Nothing is ever mutated
    -- and no row is ever removed, which is why this is a link rather than a
    -- flag: a tombstone that can be toggled is not a tombstone.
    reverses_id   uuid UNIQUE REFERENCES transactions (id) ON DELETE RESTRICT,
    reversal_kind text,

    CONSTRAINT transactions_id_is_uuid_v7 CHECK (is_uuid_v7(id)),
    CONSTRAINT transactions_reversal_is_complete
        CHECK ((reverses_id IS NULL) = (reversal_kind IS NULL)),
    CONSTRAINT transactions_reversal_kind_is_known
        CHECK (reversal_kind IS NULL OR reversal_kind IN ('correction', 'deletion')),
    CONSTRAINT transactions_do_not_reverse_themselves
        CHECK (reverses_id IS DISTINCT FROM id),

    -- The target of the composite foreign key on postings below. Redundant
    -- against the primary key as a uniqueness claim, and required as a
    -- reference target: a foreign key must point at a unique constraint over
    -- exactly its own columns.
    CONSTRAINT transactions_id_txn_date_key UNIQUE (id, txn_date)
);

COMMENT ON TABLE transactions IS
    'A set of postings that together move value without creating or destroying any.';
COMMENT ON COLUMN transactions.reverses_id IS
    'The transaction this one undoes. UNIQUE, so nothing can be reversed twice.';
COMMENT ON COLUMN transactions.reversal_kind IS
    'Why it was reversed: correction (wrong figures) or deletion (should never have existed).';

CREATE INDEX transactions_txn_date_idx ON transactions (txn_date, id);

CREATE TABLE postings (
    id             uuid          PRIMARY KEY,
    transaction_id uuid          NOT NULL,
    -- A copy of transactions.txn_date, carried here so that a balance as of a
    -- date is one index-only scan rather than a join. The composite foreign
    -- key below makes the copy incapable of drifting: a row whose date does
    -- not match its own transaction cannot be inserted, and ON UPDATE RESTRICT
    -- stops the date moving out from under it afterwards. Denormalisation is
    -- safe exactly when the database refuses to let it rot.
    txn_date       date          NOT NULL,
    -- Reproduces the order the lines were written in, and nothing else. It is
    -- never a reference: the seventh invariant of section 5 is that nothing is
    -- pointed at by its position, and PostingID is the only way to name a
    -- posting. This column exists so a transaction reads back the way it was
    -- written, which is what makes the round-trip test an equality.
    ordinal        smallint      NOT NULL,
    account_id     uuid          NOT NULL REFERENCES accounts (id) ON DELETE RESTRICT,
    -- Section 4.4: minor units as an exact integer, always beside its
    -- commodity. Two columns, never one.
    amount         numeric(40,0) NOT NULL,
    commodity_code text          NOT NULL REFERENCES commodities (code) ON DELETE RESTRICT,

    -- The exchange rate at the moment of the transaction (section 5.4), stored
    -- as an exact reduced fraction. A decimal numeric cannot hold one third,
    -- and section 4.1 forbids the float that could hold an approximation of
    -- it. These are not money, so they carry no numeric(40,0) bound; they are
    -- held to being whole numbers instead.
    rate_base      text          REFERENCES commodities (code) ON DELETE RESTRICT,
    rate_quote     text          REFERENCES commodities (code) ON DELETE RESTRICT,
    rate_num       numeric,
    rate_den       numeric,

    memo           text          NOT NULL DEFAULT '',
    -- Line-level provenance for a reversal: which line this line undoes.
    reverses_posting_id uuid UNIQUE REFERENCES postings (id) ON DELETE RESTRICT,

    CONSTRAINT postings_id_is_uuid_v7 CHECK (is_uuid_v7(id)),

    CONSTRAINT postings_carry_their_transactions_date
        FOREIGN KEY (transaction_id, txn_date)
        REFERENCES transactions (id, txn_date)
        ON UPDATE RESTRICT ON DELETE RESTRICT,

    CONSTRAINT postings_ordinal_is_not_negative CHECK (ordinal >= 0),
    CONSTRAINT postings_ordinal_is_unique_within_its_transaction
        UNIQUE (transaction_id, ordinal),

    -- A rate is present in all four columns or in none of them.
    CONSTRAINT postings_rate_is_whole_or_absent
        CHECK (num_nulls(rate_base, rate_quote, rate_num, rate_den) IN (0, 4)),
    CONSTRAINT postings_rate_is_positive
        CHECK (rate_num IS NULL OR (rate_num > 0 AND rate_den > 0)),
    CONSTRAINT postings_rate_terms_are_whole
        CHECK (rate_num IS NULL OR (scale(rate_num) = 0 AND scale(rate_den) = 0)),
    -- Stored in lowest terms, so one rate has exactly one representation and
    -- two equal rates compare equal as rows.
    CONSTRAINT postings_rate_is_reduced
        CHECK (rate_num IS NULL OR gcd(rate_num, rate_den) = 1),
    -- ledger.NewPosting: a rate prices the commodity of its own posting.
    CONSTRAINT postings_rate_prices_its_own_commodity
        CHECK (rate_base IS NULL OR rate_base = commodity_code),
    CONSTRAINT postings_rate_is_between_two_commodities
        CHECK (rate_base IS NULL OR rate_base <> rate_quote),

    CONSTRAINT postings_do_not_reverse_themselves
        CHECK (reverses_posting_id IS DISTINCT FROM id)
);

COMMENT ON TABLE postings IS
    'One line of a transaction. Immutable: corrections are new reversing lines, never edits.';
COMMENT ON COLUMN postings.txn_date IS
    'Copy of transactions.txn_date, held true by the composite foreign key. Never written independently.';
COMMENT ON COLUMN postings.ordinal IS
    'Author ordering, for display only. Never a reference: postings are named by id.';

-- The one index every balance query runs on. Covering, so a balance is
-- answered from the index alone without touching the heap: the predicate is
-- (account_id, txn_date) and the payload is what gets summed.
CREATE INDEX postings_account_date_idx
    ON postings (account_id, txn_date) INCLUDE (amount, commodity_code);

-- Lookups by transaction are served by the UNIQUE (transaction_id, ordinal)
-- index above, so no separate index on transaction_id is created here.

-- ---------------------------------------------------------------------------
-- The balance invariant, enforced by the database
--
-- SCOPE, AND ITS LIMIT. This trigger may enforce the invariants of CLAUDE.md
-- section 5 and nothing else. No budget rule, no category rule, no derived
-- figure, no calculation of any kind. Business logic lives in internal/ledger.
-- This exists because 5.1 is an accounting invariant rather than a business
-- rule, and an invariant guarded in one layer alone is one the next layer
-- breaks: the importer, the rule engine and psql will all write to these
-- tables one day, and none of them goes through NewTransaction.
--
-- Anything beyond section 5 appearing in this function is a bug, whatever it
-- happens to do.
-- ---------------------------------------------------------------------------

CREATE FUNCTION assert_transaction_balances(target uuid) RETURNS void
    LANGUAGE plpgsql
    SET search_path = pg_catalog, public, pg_temp
    AS $fn$
DECLARE
    line_count integer;
    residual   record;
BEGIN
    -- Nothing to say about a transaction that is no longer there.
    IF NOT EXISTS (SELECT 1 FROM transactions WHERE id = target) THEN
        RETURN;
    END IF;

    SELECT count(*) INTO line_count FROM postings WHERE transaction_id = target;
    IF line_count < 2 THEN
        RAISE EXCEPTION
            'transaction % has % posting(s); a transaction needs at least 2',
            target, line_count
            USING ERRCODE = 'check_violation',
                  CONSTRAINT = 'transaction_is_balanced';
    END IF;

    -- Section 5.1: every commodity totals exactly zero. No tolerance and no
    -- rounding allowance. A cross-commodity transaction balances within each
    -- commodity through an explicit pair of conversion postings.
    SELECT commodity_code, sum(amount) AS total
      INTO residual
      FROM postings
     WHERE transaction_id = target
     GROUP BY commodity_code
    HAVING sum(amount) <> 0
     ORDER BY commodity_code
     LIMIT 1;

    IF FOUND THEN
        RAISE EXCEPTION
            'transaction % is short by % %',
            target, residual.total, residual.commodity_code
            USING ERRCODE = 'check_violation',
                  CONSTRAINT = 'transaction_is_balanced';
    END IF;
END;
$fn$;

COMMENT ON FUNCTION assert_transaction_balances(uuid) IS
    'Enforces CLAUDE.md 5.1 only: at least two postings, every commodity summing to zero.';

CREATE FUNCTION assert_balanced_from_posting() RETURNS trigger
    LANGUAGE plpgsql
    SET search_path = pg_catalog, public, pg_temp
    AS $fn$
BEGIN
    -- NEW is unassigned on DELETE and OLD on INSERT, so neither can be read
    -- unconditionally. An UPDATE that moved a line between transactions would
    -- leave two transactions to answer for, hence both branches.
    IF TG_OP <> 'INSERT' THEN
        PERFORM assert_transaction_balances(OLD.transaction_id);
    END IF;
    IF TG_OP <> 'DELETE' THEN
        PERFORM assert_transaction_balances(NEW.transaction_id);
    END IF;
    RETURN NULL;
END;
$fn$;

CREATE FUNCTION assert_balanced_from_transaction() RETURNS trigger
    LANGUAGE plpgsql
    SET search_path = pg_catalog, public, pg_temp
    AS $fn$
BEGIN
    -- Without this the posting trigger would never fire for a transaction
    -- inserted with no lines at all, and an empty transaction would commit.
    PERFORM assert_transaction_balances(NEW.id);
    RETURN NULL;
END;
$fn$;

-- DEFERRABLE INITIALLY DEFERRED is what makes the invariant checkable at all:
-- the header and its lines arrive as separate statements, so a transaction is
-- legitimately unbalanced partway through being written and must only be
-- judged at COMMIT.
--
-- ---------------------------------------------------------------------------
-- IF YOU ARE WRITING AN IMPORTER, READ THIS.
--
-- Deferring has a cost that only shows up in bulk, and it shows up in the
-- worst possible shape: **the COMMIT hangs, with no error and no progress.**
-- Anyone who hits it will conclude the application is broken rather than that
-- they crossed a threshold, because nothing tells them otherwise.
--
-- Every header and every posting queues an after-trigger event that PostgreSQL
-- holds in memory until COMMIT, and that queue does not scale linearly:
--
--     300.000 events   COMMIT in about 8 seconds
--     750.000 events   had not finished after 13 minutes
--
-- The answer is not to weaken or disable this trigger. It is to load in
-- chunks. **About 5.000 transactions per database transaction is measured to
-- work** — 500.000 postings load in roughly 20 seconds that way. The only
-- thing the constraint requires is that a transaction's header and all of its
-- lines land inside the same chunk.
--
-- See TestBalanceLatencyStaysWithinItsBudget for a working chunked loader, and
-- the M2a notes in CLAUDE.md section 13 for how this was found.
-- ---------------------------------------------------------------------------
CREATE CONSTRAINT TRIGGER postings_stay_balanced
    AFTER INSERT OR UPDATE OR DELETE ON postings
    DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW EXECUTE FUNCTION assert_balanced_from_posting();

CREATE CONSTRAINT TRIGGER transactions_stay_balanced
    AFTER INSERT OR UPDATE ON transactions
    DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW EXECUTE FUNCTION assert_balanced_from_transaction();

UPDATE schema_meta SET version = 4, applied_at = now();
