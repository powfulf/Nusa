-- response_body was jsonb, and jsonb is not a byte store.
--
-- The column exists to be handed back to a caller exactly as the first attempt
-- sent it. jsonb cannot do that, and it is not a defect in jsonb: it parses a
-- document into a decomposed binary form, which is what makes it fast to query
-- and index. What comes back is an equivalent document rather than the same
-- one. Measured against PostgreSQL 16, storing
--
--     {"z": 1,  "a"  :  "two", "a": "three"}
--
-- reads back as
--
--     {"a": "three", "z": 1}
--
-- Keys reordered, insignificant whitespace dropped, the duplicate key
-- discarded. Every one of those is legal and documented, and every one of them
-- breaks the only promise this column makes.
--
-- It matters less than it looks for a client that parses JSON and more than it
-- looks for one that does not: a caller comparing the response to what it
-- received the first time, hashing it for a cache, or checking it against a
-- signature sees two different answers to one request. A replay that returns
-- an equivalent document has replayed the meaning and not the response.
--
-- json stores the text as given and still refuses a document that is not JSON,
-- which is the whole of what is wanted here. Nothing queries into this column,
-- nothing indexes it, and no JSON operator is ever applied to it — so the
-- decomposition jsonb performs is paid for and never used.
--
-- Nothing writes the column yet, so this is still free to change, exactly as
-- migration 9 was. That is not luck twice over: it is the reason the HTTP
-- layer was written before the endpoints that use it.

ALTER TABLE idempotency_keys
    ALTER COLUMN response_body TYPE json USING response_body::json;

COMMENT ON COLUMN idempotency_keys.response_body IS
    'The document the first attempt answered with, stored verbatim. json rather than jsonb: this column is handed back byte for byte and is never queried, and jsonb would reorder keys and drop whitespace (migration 10).';

UPDATE schema_meta SET version = 10, applied_at = now();
