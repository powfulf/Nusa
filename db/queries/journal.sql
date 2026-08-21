-- name: InsertTransaction :exec
INSERT INTO transactions (
    id, txn_date, occurred_at, timezone, payee, memo, reverses_id, reversal_kind
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8);

-- name: InsertPosting :exec
INSERT INTO postings (
    id, transaction_id, txn_date, ordinal, account_id, amount, commodity_code,
    rate_base, rate_quote, rate_num, rate_den, memo, reverses_posting_id
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13);

-- name: GetTransaction :one
SELECT id, txn_date, occurred_at, timezone, payee, memo, reverses_id, reversal_kind
  FROM transactions WHERE id = $1;

-- name: ListPostingsByTransaction :many
-- Ordered by the ordinal the author wrote, which is the whole reason that
-- column exists. Served by the same unique index that enforces it.
SELECT id, transaction_id, txn_date, ordinal, account_id, amount, commodity_code,
       rate_base, rate_quote, rate_num, rate_den, memo, reverses_posting_id
  FROM postings
 WHERE transaction_id = $1
 ORDER BY ordinal;

-- name: ListTransactionsBetween :many
-- Civil dates, both counted. Only txn_date decides; occurred_at is never
-- consulted, so the answer is the same for every reader in every zone.
SELECT id, txn_date, occurred_at, timezone, payee, memo, reverses_id, reversal_kind
  FROM transactions
 WHERE txn_date >= $1 AND txn_date <= $2
 ORDER BY txn_date, id;

-- name: GetPostingOwner :one
-- Widens a posting back to its transaction, which is how a lot recovers the
-- transaction that opened it.
SELECT transaction_id FROM postings WHERE id = $1;

-- name: CountTransactions :one
SELECT count(*) FROM transactions;

-- The balance queries below are the whole of CLAUDE.md 5.2 as it reaches the
-- database: an account balance is the sum of its postings and nothing else.
-- There is no cached figure anywhere to drift from them.
--
-- All-time and as-of are separate statements rather than one query with an
-- optional date, because a nullable predicate (date IS NULL OR txn_date <= date)
-- is not sargable and would cost the index-only scan both of them depend on.

-- name: AccountBalance :many
SELECT commodity_code, sum(amount)::numeric AS total
  FROM postings
 WHERE account_id = $1
 GROUP BY commodity_code
 ORDER BY commodity_code;

-- name: AccountBalanceAsOf :many
SELECT commodity_code, sum(amount)::numeric AS total
  FROM postings
 WHERE account_id = $1 AND txn_date <= $2
 GROUP BY commodity_code
 ORDER BY commodity_code;

-- name: AccountBalanceBetween :many
SELECT commodity_code, sum(amount)::numeric AS total
  FROM postings
 WHERE account_id = $1 AND txn_date >= $2 AND txn_date <= $3
 GROUP BY commodity_code
 ORDER BY commodity_code;

-- name: SubtreeBalanceAsOf :many
WITH RECURSIVE subtree(id) AS (
    SELECT a.id FROM accounts a WHERE a.id = $1
    UNION ALL
    SELECT a.id FROM accounts a JOIN subtree s ON a.parent_id = s.id
)
SELECT p.commodity_code, sum(p.amount)::numeric AS total
  FROM postings p
 WHERE p.account_id IN (SELECT s.id FROM subtree s)
   AND p.txn_date <= $2
 GROUP BY p.commodity_code
 ORDER BY p.commodity_code;

-- name: TotalsByCommodity :many
-- The whole-book form of 5.1. It must always be zero: each transaction sums to
-- zero on its own, so any number of them still do. A non-zero total means
-- something got in without passing NewTransaction.
SELECT commodity_code, sum(amount)::numeric AS total
  FROM postings
 GROUP BY commodity_code
 ORDER BY commodity_code;
