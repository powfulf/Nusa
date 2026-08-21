-- Accounts: one place value can sit or pass through.

CREATE TABLE accounts (
    id             uuid    PRIMARY KEY,
    parent_id      uuid    REFERENCES accounts (id) ON DELETE RESTRICT,
    kind           text    NOT NULL,
    name           text    NOT NULL,
    -- NULL means the account may hold several commodities, which is normal for
    -- a brokerage account holding cash and shares at once. The domain spells
    -- the same thing as an empty CommodityCode; NULL is the honest column form
    -- of "no restriction", and it keeps the foreign key meaningful.
    commodity_code text    REFERENCES commodities (code) ON DELETE RESTRICT,
    closed         boolean NOT NULL DEFAULT false,

    CONSTRAINT accounts_id_is_uuid_v7 CHECK (is_uuid_v7(id)),
    CONSTRAINT accounts_kind_is_known
        CHECK (kind IN ('asset', 'liability', 'equity', 'income', 'expense')),
    -- The domain rejects a name that is blank after strings.TrimSpace, which
    -- trims Unicode whitespace; btrim trims ASCII only. Weaker on purpose: a
    -- storage check that is stricter than the domain would refuse to persist a
    -- value the domain considers valid, which is the failure that has no fix.
    CONSTRAINT accounts_name_is_not_blank CHECK (btrim(name) <> ''),
    CONSTRAINT accounts_is_not_its_own_parent CHECK (parent_id IS DISTINCT FROM id)
);

COMMENT ON TABLE accounts IS
    'The account tree. Cycles and parent/child kind agreement are enforced by ledger.NewAccountTree, not here.';

COMMENT ON COLUMN accounts.commodity_code IS
    'The single commodity this account may hold, or NULL for no restriction.';

-- Walking to the children of an account, which is what the recursive CTE
-- behind a subtree balance does on every step.
CREATE INDEX accounts_parent_id_idx ON accounts (parent_id);

UPDATE schema_meta SET version = 3, applied_at = now();
