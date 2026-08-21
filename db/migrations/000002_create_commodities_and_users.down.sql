DROP TABLE IF EXISTS users;
DROP TABLE IF EXISTS commodities;
DROP FUNCTION IF EXISTS is_uuid_v7(uuid);

UPDATE schema_meta SET version = 1, applied_at = now();
