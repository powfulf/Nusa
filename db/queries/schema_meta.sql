-- name: GetSchemaVersion :one
-- Reads the schema generation. Doubles as the health endpoint's proof that the
-- connection works and that migrations have been applied.
SELECT version FROM schema_meta WHERE singleton;
