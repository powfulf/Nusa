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
-- A lot's remaining quantity is a running position rather than a record of an
-- event, so it moves. What moved it is not lost: every movement appends a row
-- to lot_consumptions, and summing those reconstructs this figure from scratch.
-- The same relationship section 5.2 describes between a cached balance and the
-- postings it comes from.
UPDATE lots SET remaining_amount = $2 WHERE id = $1;

-- name: InsertLotConsumption :exec
-- One lot's part in one disposing line. Section 4.7: the basis is an exact
-- fraction, never rounded on the way in.
INSERT INTO lot_consumptions (
    posting_id, lot_id,
    quantity_amount, quantity_commodity,
    basis_num, basis_den, basis_commodity
) VALUES ($1, $2, $3, $4, $5, $6, $7);

-- name: ListConsumptionsByPosting :many
-- What one disposing line drew on. Ordered by the lots' own FIFO ordering, so
-- a disposal reads back in the order it consumed — that order is derived from
-- the lots rather than stored, because it is a trace of the selection and not
-- a fact about the disposal.
SELECT c.posting_id, c.lot_id,
       c.quantity_amount, c.quantity_commodity,
       c.basis_num, c.basis_den, c.basis_commodity
  FROM lot_consumptions c
  JOIN lots l ON l.id = c.lot_id
 WHERE c.posting_id = $1
 ORDER BY l.opened_on, l.id;

-- name: ListConsumptionsByLot :many
-- Everything that has ever drawn on one lot. This is what reconstructs
-- remaining_amount from the appended record rather than trusting the running
-- figure, which is the drift check the M2a notes ask for wherever a cached
-- number exists.
SELECT posting_id, lot_id,
       quantity_amount, quantity_commodity,
       basis_num, basis_den, basis_commodity
  FROM lot_consumptions
 WHERE lot_id = $1
 ORDER BY posting_id;

-- name: ListOpenLotsByAccountForUpdate :many
-- The same reading as ListOpenLotsByAccount, taking a row lock on every lot it
-- returns.
--
-- A disposal reads the open lots, decides what to consume, and writes the
-- reduced figures back. Two disposals on one account running at the same time
-- would otherwise both read the same lots, both find them sufficient, and both
-- commit — leaving the holding consumed twice and remaining_amount describing
-- neither. The lock makes the second one wait, re-read, and correctly run out
-- of units.
SELECT id, account_id, opened_by, opened_on,
       quantity_amount, quantity_commodity,
       remaining_amount, remaining_commodity,
       cost_amount, cost_commodity
  FROM lots
 WHERE account_id = $1 AND remaining_amount > 0
 ORDER BY opened_on, id
   FOR UPDATE;
