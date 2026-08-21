-- Lots: one acquisition of an asset, kept apart from every other acquisition
-- so that a later disposal can say which units it disposed of.

CREATE TABLE lots (
    id         uuid PRIMARY KEY,
    account_id uuid NOT NULL REFERENCES accounts (id) ON DELETE RESTRICT,
    -- The posting that acquired it, not the transaction. A transaction can
    -- acquire two things at once, and "which line was this lot" would then
    -- have no answer. The owning transaction stays recoverable through
    -- postings.transaction_id, so the narrower fact loses nothing and the two
    -- can never disagree.
    opened_by  uuid NOT NULL REFERENCES postings (id) ON DELETE RESTRICT,
    -- The only thing that orders lots against each other.
    opened_on  date NOT NULL,

    -- Three amounts, each beside its own commodity (section 4.4). Quantity and
    -- remaining share a commodity by construction and are still stored as
    -- pairs, because the alternative is a bare amount column whose commodity
    -- lives somewhere else, which is the shape that rule exists to forbid. A
    -- check keeps the pair honest instead.
    quantity_amount     numeric(40,0) NOT NULL,
    quantity_commodity  text          NOT NULL REFERENCES commodities (code) ON DELETE RESTRICT,
    remaining_amount    numeric(40,0) NOT NULL,
    remaining_commodity text          NOT NULL REFERENCES commodities (code) ON DELETE RESTRICT,
    -- What the whole of quantity cost, in the currency it was paid in. A
    -- total, not a price per unit: a total is exact and a per-unit price
    -- generally is not.
    cost_amount         numeric(40,0) NOT NULL,
    cost_commodity      text          NOT NULL REFERENCES commodities (code) ON DELETE RESTRICT,

    CONSTRAINT lots_id_is_uuid_v7 CHECK (is_uuid_v7(id)),
    -- ledger.NewLot: a lot is an acquisition, so quantity is positive.
    CONSTRAINT lots_quantity_is_positive CHECK (quantity_amount > 0),
    CONSTRAINT lots_cost_is_not_negative CHECK (cost_amount >= 0),
    CONSTRAINT lots_remaining_is_within_quantity
        CHECK (remaining_amount >= 0 AND remaining_amount <= quantity_amount),
    CONSTRAINT lots_remaining_matches_quantity_commodity
        CHECK (remaining_commodity = quantity_commodity),
    -- ledger.NewLot again: an asset and what it cost are different things, so
    -- they are never the same commodity.
    CONSTRAINT lots_cost_is_a_different_commodity
        CHECK (cost_commodity <> quantity_commodity)
);

COMMENT ON TABLE lots IS
    'One acquisition of an asset, with what it cost. FIFO ordering is (opened_on, id).';
COMMENT ON COLUMN lots.opened_by IS
    'The posting that acquired this lot. A posting, never a transaction.';

-- FIFO consumption reads a holding oldest-first, and the tie on id is what
-- stops the same disposal computing a different gain on a different run.
CREATE INDEX lots_account_opened_idx ON lots (account_id, opened_on, id);

-- Finding the lot a given line opened, which is how a reversal or an audit
-- entry gets from a posting back to what it created.
CREATE INDEX lots_opened_by_idx ON lots (opened_by);

UPDATE schema_meta SET version = 5, applied_at = now();
