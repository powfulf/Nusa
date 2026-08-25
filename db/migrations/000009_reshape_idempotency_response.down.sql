-- Back to migration 6's shape: one jsonb column named response, no status.

ALTER TABLE idempotency_keys
    DROP CONSTRAINT IF EXISTS idempotency_keys_response_status_is_http,
    DROP CONSTRAINT IF EXISTS idempotency_keys_response_is_whole_or_absent;

ALTER TABLE idempotency_keys DROP COLUMN IF EXISTS response_status;

ALTER TABLE idempotency_keys RENAME COLUMN response_body TO response;

-- The comment migration 9 set on the renamed column does not belong to
-- migration 6's version of it, so it is removed rather than left behind
-- describing a rename that has been undone.
COMMENT ON COLUMN idempotency_keys.response IS NULL;

UPDATE schema_meta SET version = 8, applied_at = now();
