-- What a disposal actually consumed: which lots, how much of each, and what
-- that much had cost.
--
-- WHY THIS TABLE EXISTS. Until now a disposal reduced lots.remaining_amount
-- and left no record of having done so. The running figure was correct and the
-- history behind it was gone: after the fact, nothing could say which
-- acquisitions a sale had drawn on, from which dates, at which prices.
--
-- That is unreconstructible rather than merely inconvenient. FIFO selection
-- depends on what was open at the moment of the disposal, and later
-- acquisitions and disposals change that. Re-running the selection over
-- today's lots does not reproduce the answer given last year, and no amount of
-- care later recovers it.
--
-- It is also the one place the ledger was quietly overwriting itself. Section
-- 5.3 makes the book append-only: corrections are reversing entries and a
-- deletion is a tombstone. A remaining quantity that moves with no record of
-- what moved it is the exception that rule exists to forbid, hidden inside an
-- UPDATE. The running figure stays — it is a position, and positions are
-- allowed to move — but every movement is now an appended row, and the
-- position is reconstructible from them.
--
-- Section 7 is why it matters to a person rather than to an accountant: the
-- user is shown "on-paper gain — not money until you sell", and when they do
-- sell they are owed an answer to "why is the gain this number". That answer
-- is these rows.

-- The two composite unique constraints below are the reference targets for the
-- foreign keys in lot_consumptions. Both are redundant against lots' primary
-- key as uniqueness claims and required as targets: a foreign key must point
-- at a unique constraint over exactly its own columns. It is the same
-- arrangement postings uses to keep its copy of txn_date from drifting.
ALTER TABLE lots
    ADD CONSTRAINT lots_id_quantity_commodity_key UNIQUE (id, quantity_commodity),
    ADD CONSTRAINT lots_id_cost_commodity_key     UNIQUE (id, cost_commodity);

CREATE TABLE lot_consumptions (
    -- The line that disposed of the units, not the transaction that contained
    -- it. A transaction can sell two holdings at once, and "which line drew on
    -- this lot" would then have no answer — the same reasoning that puts
    -- opened_by on lots.
    posting_id uuid NOT NULL REFERENCES postings (id) ON DELETE RESTRICT,
    lot_id     uuid NOT NULL REFERENCES lots (id)     ON DELETE RESTRICT,

    -- How much came out of that lot. Section 4.4: an amount is never stored
    -- without its commodity beside it.
    quantity_amount    numeric(40,0) NOT NULL,
    quantity_commodity text          NOT NULL REFERENCES commodities (code) ON DELETE RESTRICT,

    -- What that quantity had cost: ledger.Consumption.Basis, which is a
    -- ledger.Rat and NOT a Money.
    --
    -- WHY THIS IS A FRACTION AND NOT A numeric LIKE EVERY OTHER AMOUNT.
    --
    -- Sell 0,1 BTC out of a 0,3 BTC lot that cost Rp 1.000.000, and the basis
    -- is 100.000.000 / 3 minor units — 33.333.333,33… — which no numeric(40,0)
    -- can hold.
    --
    -- Rounding it to 33.333.333 would be rounding in the middle of a
    -- calculation, invisibly, once per consumed lot, and the rounded pieces
    -- would no longer add back up to the million that was actually paid.
    -- Section 4.6 puts rounding at the last step only, and section 4.7 keeps
    -- anything that cannot stay a whole number of smallest units in
    -- ledger.Rat until Round is called on it deliberately. A stored basis is
    -- not the last step: a realised gain sums several of these and rounds
    -- once, at the end.
    --
    -- So it is a reduced fraction, exactly as a rate is, for exactly the same
    -- reason: a decimal cannot hold one third and section 4.1 forbids the
    -- float that could hold an approximation of it.
    basis_num       numeric NOT NULL,
    basis_den       numeric NOT NULL,
    basis_commodity text    NOT NULL REFERENCES commodities (code) ON DELETE RESTRICT,

    -- One row per lot per disposing line. ConsumeFIFO draws on each lot at
    -- most once, so this is the natural key and no identity has to be invented
    -- for it.
    --
    -- Section 5.7 requires an identity of its own for everything that can be
    -- pointed at. Nothing points at a consumption: it is the join between a
    -- line and a lot, both of which already have identities, and a reversal or
    -- an audit entry names those rather than this. Minting an id here would be
    -- inventing something for the domain to carry that nothing would ever use.
    PRIMARY KEY (posting_id, lot_id),

    -- ledger.Lot.Consume: you consume a positive quantity or you consume
    -- nothing. A zero row would be a disposal that drew on a lot without
    -- taking anything from it.
    CONSTRAINT lot_consumptions_quantity_is_positive CHECK (quantity_amount > 0),

    -- The basis follows big.Rat's normal form, which is what ledger.Rat holds:
    -- a positive denominator, whole terms, in lowest terms. One value then has
    -- exactly one representation, and two equal bases compare equal as rows.
    CONSTRAINT lot_consumptions_basis_is_not_negative CHECK (basis_num >= 0),
    CONSTRAINT lot_consumptions_basis_denominator_is_positive CHECK (basis_den > 0),
    CONSTRAINT lot_consumptions_basis_terms_are_whole
        CHECK (scale(basis_num) = 0 AND scale(basis_den) = 0),
    CONSTRAINT lot_consumptions_basis_is_reduced
        CHECK (gcd(basis_num, basis_den) = 1),

    -- A consumption cannot claim a commodity its lot does not hold. Both are
    -- copies of something lots already knows, and both are held true by the
    -- database rather than by whoever writes the row.
    CONSTRAINT lot_consumptions_quantity_matches_its_lot
        FOREIGN KEY (lot_id, quantity_commodity)
        REFERENCES lots (id, quantity_commodity)
        ON UPDATE RESTRICT ON DELETE RESTRICT,
    CONSTRAINT lot_consumptions_basis_matches_its_lots_cost
        FOREIGN KEY (lot_id, basis_commodity)
        REFERENCES lots (id, cost_commodity)
        ON UPDATE RESTRICT ON DELETE RESTRICT
);

COMMENT ON TABLE lot_consumptions IS
    'Which lots a disposing line drew on, how much of each, and what that much cost.';
COMMENT ON COLUMN lot_consumptions.basis_num IS
    'Numerator of the exact cost basis. A fraction, never rounded here (4.6, 4.7).';
COMMENT ON COLUMN lot_consumptions.posting_id IS
    'The disposing line. A posting, never a transaction.';

-- Reading a lot's history: everything that has ever drawn on it, which is what
-- reconstructs remaining_amount from the appended record.
CREATE INDEX lot_consumptions_lot_idx ON lot_consumptions (lot_id);

-- Consumption order is deliberately not stored. FIFO order is (opened_on, id)
-- on the lots themselves, so it is derivable; and more fundamentally the order
-- in which a policy selected lots is a trace of the algorithm rather than a
-- fact about the disposal. The set of (lot, quantity, basis) determines the
-- gain completely, whichever order they were chosen in. Which policy chose
-- them is a separate question, and a real one once Country Packs bring
-- policies other than FIFO; it is not answered here, and adding a column for
-- it before any such policy exists would be guessing at its shape.

UPDATE schema_meta SET version = 7, applied_at = now();
