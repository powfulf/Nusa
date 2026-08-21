-- name: InsertUser :exec
INSERT INTO users (id) VALUES ($1);

-- name: ClaimIdempotencyKey :one
-- Claims a key, or reports that someone already holds a live one.
--
-- No row comes back when the key is held and has not expired, which is the
-- signal to read the existing claim and decide between a replay and a refusal.
-- A concurrent claimer blocks here until the first transaction commits or
-- aborts, so two simultaneous replays of the same write cannot both proceed.
--
-- An expired claim is taken over rather than left to block forever, which is
-- what makes the TTL mean something for correctness and not just for the
-- sweeper.
INSERT INTO idempotency_keys (
    actor_id, key, fingerprint, entity_kind, entity_id, created_at, expires_at
) VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT (actor_id, key) DO UPDATE
    SET fingerprint = excluded.fingerprint,
        entity_kind = excluded.entity_kind,
        entity_id   = excluded.entity_id,
        response    = NULL,
        created_at  = excluded.created_at,
        expires_at  = excluded.expires_at
  WHERE idempotency_keys.expires_at <= excluded.created_at
RETURNING actor_id, key, fingerprint, entity_kind, entity_id, response, created_at, expires_at;

-- name: GetIdempotencyKey :one
SELECT actor_id, key, fingerprint, entity_kind, entity_id, response, created_at, expires_at
  FROM idempotency_keys
 WHERE actor_id = $1 AND key = $2;

-- name: DeleteExpiredIdempotencyKeys :execrows
DELETE FROM idempotency_keys WHERE expires_at <= $1;

-- name: InsertAuditEntry :exec
INSERT INTO audit_log (
    id, occurred_at, actor_id, origin, action, entity_kind, entity_id, diff
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8);

-- name: ListAuditEntriesForEntity :many
SELECT id, occurred_at, actor_id, origin, action, entity_kind, entity_id, diff
  FROM audit_log
 WHERE entity_kind = $1 AND entity_id = $2
 ORDER BY occurred_at DESC, id DESC;

-- name: CountAuditEntries :one
SELECT count(*) FROM audit_log;

-- name: InsertLot :exec
INSERT INTO lots (
    id, account_id, opened_by, opened_on,
    quantity_amount, quantity_commodity,
    remaining_amount, remaining_commodity,
    cost_amount, cost_commodity
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10);

-- name: GetLot :one
SELECT id, account_id, opened_by, opened_on,
       quantity_amount, quantity_commodity,
       remaining_amount, remaining_commodity,
       cost_amount, cost_commodity
  FROM lots WHERE id = $1;

-- name: ListOpenLotsByAccount :many
-- FIFO order: oldest first, identity breaking the tie so the same disposal
-- computes the same gain on every run.
SELECT id, account_id, opened_by, opened_on,
       quantity_amount, quantity_commodity,
       remaining_amount, remaining_commodity,
       cost_amount, cost_commodity
  FROM lots
 WHERE account_id = $1 AND remaining_amount > 0
 ORDER BY opened_on, id;

-- name: SetLotRemaining :exec
-- The one mutable figure in the schema, and it is not history: a lot's
-- remaining quantity is a running position, not a record of an event. What
-- consumed it is recorded by the disposal transaction.
UPDATE lots SET remaining_amount = $2 WHERE id = $1;
