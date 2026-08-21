DROP TABLE IF EXISTS accounts;

UPDATE schema_meta SET version = 2, applied_at = now();
