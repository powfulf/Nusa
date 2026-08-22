DROP TABLE IF EXISTS lot_consumptions;

-- Dropped in the reverse of the order they were added, and only after the
-- table whose foreign keys point at them is gone.
ALTER TABLE lots
    DROP CONSTRAINT IF EXISTS lots_id_cost_commodity_key,
    DROP CONSTRAINT IF EXISTS lots_id_quantity_commodity_key;

UPDATE schema_meta SET version = 6, applied_at = now();
