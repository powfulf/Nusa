-- name: InsertAccount :exec
INSERT INTO accounts (id, parent_id, kind, name, commodity_code, closed)
VALUES ($1, $2, $3, $4, $5, $6);

-- name: GetAccount :one
SELECT id, parent_id, kind, name, commodity_code, closed
  FROM accounts WHERE id = $1;

-- name: ListAccounts :many
-- The whole tree. Building a ledger.AccountTree needs every ancestor of every
-- account it contains, and an account tree is tens to low hundreds of rows, so
-- the whole set is the honest unit to read.
SELECT id, parent_id, kind, name, commodity_code, closed
  FROM accounts ORDER BY id;

-- name: ListAccountSubtree :many
WITH RECURSIVE subtree(id) AS (
    SELECT a.id FROM accounts a WHERE a.id = $1
    UNION ALL
    SELECT a.id FROM accounts a JOIN subtree s ON a.parent_id = s.id
)
SELECT s.id FROM subtree s ORDER BY s.id;
