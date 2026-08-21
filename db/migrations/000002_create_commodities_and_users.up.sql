-- Reference data and actors: the two things every later table points at.

-- Identities in Nusa are canonical lowercase UUIDv7, supplied by the client
-- and validated inside the domain (ledger.ValidateID). Postgres normalises a
-- uuid to canonical lowercase on input but knows nothing about versions, so a
-- v4 would slip through and disagree with the domain silently.
--
-- The version nibble is the 15th character of the canonical text form and the
-- variant nibble is the 20th:
--
--     0198e3a1-4b2c-7f3d-9a1b-2c3d4e5f6071
--                   ^15       ^20
--
-- IMMUTABLE is what allows this in a CHECK constraint; it is true, because the
-- answer depends on nothing but the argument.
CREATE FUNCTION is_uuid_v7(id uuid) RETURNS boolean
    LANGUAGE sql IMMUTABLE PARALLEL SAFE STRICT
    RETURN substring(id::text FROM 15 FOR 1) = '7'
       AND substring(id::text FROM 20 FOR 1) IN ('8', '9', 'a', 'b');

COMMENT ON FUNCTION is_uuid_v7(uuid) IS
    'Whether a uuid carries version 7 and the RFC 4122 variant. Mirrors ledger.ValidateID.';

-- Commodities. Scale lives here and nowhere else (CLAUDE.md §4.2), so two
-- amounts of the same commodity cannot disagree about where the decimal point
-- sits: neither of them carries the answer.
CREATE TABLE commodities (
    code  text     PRIMARY KEY,
    kind  text     NOT NULL,
    scale smallint NOT NULL,

    -- Mirrors ledger.validateCommodityCode: an uppercase letter, then up to 31
    -- more of A-Z 0-9 . _ - . Strictness is deliberate — "idr" quietly failing
    -- to equal "IDR" would surface as a balancing error far from its cause.
    CONSTRAINT commodities_code_is_canonical
        CHECK (code ~ '^[A-Z][A-Z0-9._-]{0,31}$'),
    CONSTRAINT commodities_kind_is_known
        CHECK (kind IN ('currency', 'equity', 'fund', 'crypto', 'metal')),
    -- ledger.MaxScale is 18: ETH's smallest unit, and the most any commodity
    -- Nusa recognises needs.
    CONSTRAINT commodities_scale_is_within_range
        CHECK (scale BETWEEN 0 AND 18)
);

COMMENT ON TABLE commodities IS
    'Everything an amount can be denominated in. Seeded from ledger.StandardRegistry; Country Packs add their own.';

-- Seeded from ledger.StandardRegistry(). These two lists are a duplicated
-- fact, so a test compares them and fails when they drift — see
-- TestSeededCommoditiesMatchTheStandardRegistry.
--
-- Deliberately not the full ISO 4217 table: core carries what a self-hoster is
-- likely to hold, and an exchange's share scale or a fund's unit scale is a
-- country-specific fact that belongs in a Country Pack (§6).
INSERT INTO commodities (code, kind, scale) VALUES
    ('AED', 'currency', 2), ('AUD', 'currency', 2), ('BHD', 'currency', 3),
    ('BRL', 'currency', 2), ('CAD', 'currency', 2), ('CHF', 'currency', 2),
    ('CNY', 'currency', 2), ('DKK', 'currency', 2), ('EUR', 'currency', 2),
    ('GBP', 'currency', 2), ('HKD', 'currency', 2), ('IDR', 'currency', 2),
    ('INR', 'currency', 2), ('JOD', 'currency', 3), ('JPY', 'currency', 0),
    ('KRW', 'currency', 0), ('KWD', 'currency', 3), ('MXN', 'currency', 2),
    ('MYR', 'currency', 2), ('NOK', 'currency', 2), ('NZD', 'currency', 2),
    ('OMR', 'currency', 3), ('PHP', 'currency', 2), ('PLN', 'currency', 2),
    ('SAR', 'currency', 2), ('SEK', 'currency', 2), ('SGD', 'currency', 2),
    ('THB', 'currency', 2), ('TRY', 'currency', 2), ('TWD', 'currency', 2),
    ('USD', 'currency', 2), ('VND', 'currency', 0), ('ZAR', 'currency', 2),
    -- Scales that are properties of the chains rather than of any country.
    ('BTC', 'crypto', 8), ('ETH', 'crypto', 18);

-- Users, minimal on purpose. M2a has no authentication; this table exists so
-- audit_log.actor_id and idempotency_keys.actor_id can be real foreign keys
-- from the first row written. M2b adds credentials by ALTER, which is cheap,
-- rather than by creating a table and migrating rows into it, which is not.
CREATE TABLE users (
    id         uuid        PRIMARY KEY,
    created_at timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT users_id_is_uuid_v7 CHECK (is_uuid_v7(id))
);

COMMENT ON TABLE users IS
    'Identity only until M2b adds credentials. Exists now so audit and idempotency can reference a real actor.';

UPDATE schema_meta SET version = 2, applied_at = now();
