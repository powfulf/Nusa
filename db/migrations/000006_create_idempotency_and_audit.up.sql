-- Idempotency and audit: the two tables that make a write safe to repeat and
-- possible to account for afterwards.

-- Section 5.6: every write is idempotent. The key is checked in the repository
-- rather than in HTTP middleware, because the importer, the rule engine and
-- the scheduler all replay writes without an HTTP request anywhere in sight.
-- A layer above may reuse this; none of them may be the only thing enforcing
-- it.
CREATE TABLE idempotency_keys (
    actor_id    uuid        NOT NULL REFERENCES users (id) ON DELETE RESTRICT,
    key         text        NOT NULL,
    -- SHA-256 over the canonical form of the request. Same key with a
    -- different body is a client bug and must be refused, not replayed: the
    -- caller believes it is retrying something it is not.
    fingerprint bytea       NOT NULL,
    -- What the first attempt produced, so a replay can answer with it rather
    -- than doing the work twice.
    entity_kind text        NOT NULL,
    entity_id   uuid        NOT NULL,
    -- Reserved for M2b, which replays the original HTTP response body.
    response    jsonb,
    created_at  timestamptz NOT NULL DEFAULT now(),
    expires_at  timestamptz NOT NULL,

    -- Scoped per actor: two people may pick the same key, and one must never
    -- receive the other's result.
    PRIMARY KEY (actor_id, key),

    CONSTRAINT idempotency_keys_key_is_not_blank CHECK (key <> ''),
    CONSTRAINT idempotency_keys_fingerprint_is_sha256
        CHECK (octet_length(fingerprint) = 32),
    CONSTRAINT idempotency_keys_entity_id_is_uuid_v7 CHECK (is_uuid_v7(entity_id)),
    CONSTRAINT idempotency_keys_expire_after_they_are_created
        CHECK (expires_at > created_at)
);

COMMENT ON TABLE idempotency_keys IS
    'Replay protection for writes. Same key and same fingerprint replays; same key and a different fingerprint is refused.';

-- Sweeping expired keys.
CREATE INDEX idempotency_keys_expires_at_idx ON idempotency_keys (expires_at);

-- Section 10: every mutation records who, when, what, and whether it came from
-- a human, a rule, an import or the AI layer.
CREATE TABLE audit_log (
    id          uuid        PRIMARY KEY,
    occurred_at timestamptz NOT NULL DEFAULT now(),
    -- NULL where no person was responsible: a scheduled rule firing overnight
    -- has an origin but no actor.
    actor_id    uuid        REFERENCES users (id) ON DELETE RESTRICT,
    -- Present from the first migration on purpose. M11 is what reads it, and
    -- adding a NOT NULL column to a table with millions of rows is a different
    -- and much worse operation than having it there from the start.
    origin      text        NOT NULL,
    action      text        NOT NULL,
    entity_kind text        NOT NULL,
    entity_id   uuid        NOT NULL,
    -- What changed. Postings are immutable, so for the ledger this is what was
    -- written rather than a before-and-after.
    diff        jsonb       NOT NULL,

    CONSTRAINT audit_log_id_is_uuid_v7 CHECK (is_uuid_v7(id)),
    CONSTRAINT audit_log_origin_is_known
        CHECK (origin IN ('human', 'rule', 'import', 'ai')),
    -- A human origin without a human is a contradiction, and the one case
    -- where a missing actor would be a silent loss of accountability.
    CONSTRAINT audit_log_human_origin_has_an_actor
        CHECK (origin <> 'human' OR actor_id IS NOT NULL),
    CONSTRAINT audit_log_action_is_not_blank CHECK (action <> ''),
    CONSTRAINT audit_log_entity_kind_is_not_blank CHECK (entity_kind <> '')
);

COMMENT ON TABLE audit_log IS
    'One row per mutation: actor, time, entity, diff, and whether a human, a rule, an import or the AI layer caused it.';
COMMENT ON COLUMN audit_log.origin IS
    'human | rule | import | ai. Written now, read by the AI layer in M11.';

-- The history of one thing, newest first, which is what an entity's audit view
-- asks for.
CREATE INDEX audit_log_entity_idx
    ON audit_log (entity_kind, entity_id, occurred_at DESC);

-- The whole log, newest first.
CREATE INDEX audit_log_occurred_at_idx ON audit_log (occurred_at DESC);

UPDATE schema_meta SET version = 6, applied_at = now();
