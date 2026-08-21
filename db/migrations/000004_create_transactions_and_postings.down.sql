DROP TABLE IF EXISTS postings;
DROP TABLE IF EXISTS transactions;
DROP FUNCTION IF EXISTS assert_balanced_from_transaction();
DROP FUNCTION IF EXISTS assert_balanced_from_posting();
DROP FUNCTION IF EXISTS assert_transaction_balances(uuid);

UPDATE schema_meta SET version = 3, applied_at = now();
