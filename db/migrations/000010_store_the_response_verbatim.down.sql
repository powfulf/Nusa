-- Back to jsonb, and back to the comment migration 9 left on the column.
--
-- The cast is lossy in one direction only: a document already stored as text
-- is normalised on the way into jsonb, so going down and up again does not
-- restore the original bytes. That is a property of the types rather than of
-- this migration, and it is why the down is here to restore the *schema*, not
-- to promise the data is unchanged by the round trip.

ALTER TABLE idempotency_keys
    ALTER COLUMN response_body TYPE jsonb USING response_body::jsonb;

COMMENT ON COLUMN idempotency_keys.response_body IS
    'The document the first attempt answered with. Renamed from "response" in migration 9, when the status was split out.';

UPDATE schema_meta SET version = 9, applied_at = now();
