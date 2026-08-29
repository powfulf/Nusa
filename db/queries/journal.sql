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

-- name: ListTransactionsPage :many
-- One page of transactions, by keyset rather than by offset.
--
-- Civil dates, both counted. Only txn_date decides; occurred_at is never
-- consulted, so the answer is the same for every reader in every zone.
--
-- The order is (txn_date, id) and the position is a row value compared
-- against that same pair, which is what makes this a keyset scan on
-- transactions_txn_date_idx rather than a sort. OFFSET would have to count
-- past every row it skips, and its cost grows with the page number; worse, an
-- insert before the offset shifts every later page by one and silently repeats
-- a row the caller has already seen.
--
-- The tie-break on id is not decoration. Several transactions routinely share
-- a civil date, and without a total order two pages can disagree about which
-- of them came first — which is a skipped row and a duplicated row in the same
-- breath, reported by nothing.
--
-- DO NOT "SIMPLIFY" THE ORDER BY TO txn_date ALONE, AND DO NOT TRUST THE TESTS
-- TO STOP YOU. Removing the id there leaves the whole suite green, and that is
-- a property of the plan rather than of the code: this query is served by an
-- Index Only Scan on transactions_txn_date_idx, which is (txn_date, id), so
-- the rows arrive in identity order whether or not the ORDER BY asks for it.
-- Confirmed with EXPLAIN, not assumed. The moment the plan changes — the index
-- dropped, a parallel or bitmap plan chosen at a size nobody has reached yet,
-- a different planner — the order becomes heap order and the cursor starts
-- skipping rows silently. Measured on a bare table where the planner chose a
-- sort instead, `ORDER BY txn_date` returned a genuinely different order from
-- `ORDER BY txn_date, id`.
--
-- So this line is correct, load-bearing, and not independently falsifiable by
-- any test here. It is recorded as such for the same reason the empty-list
-- branch in api.ClientIP is: an unfalsifiable line that nobody has labelled
-- gets deleted eventually by somebody who checked that the tests still pass.
--
-- The WHERE clause below is the half that *is* falsifiable. Comparing only the
-- date there, rather than the row value, turns the same-date pagination test
-- red — and only that test, because it is the only one whose rows share a day.
--
-- One row past the requested size is fetched so the caller can tell "this is
-- the last page" from "the next page happens to be empty" without a second
-- query.
SELECT id, txn_date, occurred_at, timezone, payee, memo, reverses_id, reversal_kind
  FROM transactions
 WHERE (sqlc.narg(from_date)::date IS NULL OR txn_date >= sqlc.narg(from_date)::date)
   AND (sqlc.narg(to_date)::date IS NULL OR txn_date <= sqlc.narg(to_date)::date)
   AND (sqlc.narg(after_date)::date IS NULL
        OR (txn_date, id) > (sqlc.narg(after_date)::date, sqlc.narg(after_id)::uuid))
 ORDER BY txn_date, id
 LIMIT sqlc.arg(row_limit);

-- name: ListPostingsByTransactions :many
-- Every posting of one or more transactions, ordered by the ordinal the author
-- wrote — which is the whole reason that column exists.
--
-- It serves the single-transaction case too, with a one-element array. Two
-- queries differing only in their cardinality would mean two copies of the
-- row-to-domain conversion behind them, and that conversion is where a posting
-- either survives the round trip or quietly does not.
--
-- Reading a page of fifty transactions and then asking for each one's postings
-- separately would be fifty-one round trips for one answer. The ordering
-- carries the grouping, so a caller walks the result once and cuts it into
-- transactions without sorting anything.
SELECT id, transaction_id, txn_date, ordinal, account_id, amount, commodity_code,
       rate_base, rate_quote, rate_num, rate_den, memo, reverses_posting_id
  FROM postings
 WHERE transaction_id = ANY(sqlc.arg(transaction_ids)::uuid[])
 ORDER BY transaction_id, ordinal;

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
