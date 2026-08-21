DROP TABLE IF EXISTS lots;

UPDATE schema_meta SET version = 4, applied_at = now();
