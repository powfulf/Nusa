-- The column migration 6 reserved for M2b does not fit what M2b needs.
--
-- It reads `response jsonb`, commented "reserved for M2b, which replays the
-- original HTTP response body". Checked against what a replay actually has to
-- reproduce, a body is not enough: the status line is part of the response and
-- is not derivable from the body. A create that answered 201 and replays as
-- 200 has not replayed. So has a request that failed validation with 422 and
-- comes back 200 carrying the error document as though it were a result.
--
-- Nothing writes the column yet — the HTTP layer does not exist — so this is
-- the last moment it is free to change. Reshaping it after the first
-- deployment would mean either a backfill with a guessed status or a period
-- where replays are wrong, which is exactly the situation the M2a note about
-- columns being cheap now and expensive later was describing.
--
-- Migration 6 is left as it was written. Migrations are forward-only (section
-- 3), and editing an applied one is how two databases claiming the same
-- version come to differ.

ALTER TABLE idempotency_keys RENAME COLUMN response TO response_body;

ALTER TABLE idempotency_keys
    ADD COLUMN response_status smallint;

COMMENT ON COLUMN idempotency_keys.response_status IS
    'The HTTP status the first attempt answered with. Not derivable from the body, so it is stored.';
COMMENT ON COLUMN idempotency_keys.response_body IS
    'The document the first attempt answered with. Renamed from "response" in migration 9, when the status was split out.';

ALTER TABLE idempotency_keys
    -- Both or neither. A claim is written before the work is done, so a live
    -- row legitimately has no response yet; what must never exist is half of
    -- one, because a replay would then have to invent the missing half.
    ADD CONSTRAINT idempotency_keys_response_is_whole_or_absent
        CHECK ((response_status IS NULL) = (response_body IS NULL)),

    ADD CONSTRAINT idempotency_keys_response_status_is_http
        CHECK (response_status IS NULL OR response_status BETWEEN 100 AND 599);

-- No column is added for response headers, and that is a decision rather than
-- an omission. The one header a replay has to reproduce is Location on a 201,
-- and it is already reconstructible: entity_kind and entity_id are stored, and
-- the route for a kind is a fact about the API rather than about the request.
-- Storing the string as well would be a second copy of something derivable,
-- free to drift the first time a route changes.

UPDATE schema_meta SET version = 9, applied_at = now();
