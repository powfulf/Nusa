DROP TABLE IF EXISTS audit_log;
DROP TABLE IF EXISTS idempotency_keys;

UPDATE schema_meta SET version = 5, applied_at = now();
