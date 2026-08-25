-- Credentials, sessions and second factors.

-- name: InsertCredentialedUser :exec
-- Registers a person.
--
-- Whether registration is open is not asked here, and deliberately so. The
-- users_at_most_one_credentialed index makes a second credentialed row
-- impossible, so a concurrent pair of registrations against an empty instance
-- resolves in the database rather than in a check the caller could forget or
-- race. The second inserter blocks on the uncommitted index entry until the
-- first transaction resolves, then fails with a unique violation.
INSERT INTO users (id, email, password_hash, password_changed_at, created_at)
VALUES ($1, $2, $3, $4, $5);

-- name: GetCredentialsByEmail :one
-- Case-insensitive, matching the users_email_key index expression exactly so
-- the lookup uses it rather than scanning.
SELECT id, email, password_hash, password_changed_at, created_at
  FROM users
 WHERE lower(email) = lower(sqlc.arg(email)::text);

-- name: GetCredentialsByID :one
SELECT id, email, password_hash, password_changed_at, created_at
  FROM users
 WHERE id = $1;

-- name: CountCredentialedUsers :one
-- Whether this instance has anybody yet. Used to decide what a registration
-- form should offer, never to decide whether a registration may proceed —
-- that answer would be stale the moment it was read.
SELECT count(*) FROM users WHERE email IS NOT NULL;

-- name: UpdatePasswordHash :execrows
UPDATE users
   SET password_hash = $2, password_changed_at = $3
 WHERE id = $1 AND password_hash IS NOT NULL;

-- ---------------------------------------------------------------------------
-- Sessions
-- ---------------------------------------------------------------------------

-- name: InsertSession :exec
INSERT INTO sessions (
    id, user_id, token_hash, created_at, last_seen_at, expires_at, authenticated_at
) VALUES ($1, $2, $3, $4, $4, $5, $6);

-- name: GetSessionByTokenHash :one
-- The lookup on every authenticated request.
--
-- The row comes back whatever its state, and the repository decides. Three
-- things end a session — expiry, revocation, and a password changed since it
-- began — and they are one answer to whoever holds the cookie, so they are
-- resolved in one place rather than by every caller remembering all three.
--
-- Awaiting a second factor is not one of them. Such a session is returned, and
-- must be: it is the credential the second step itself presents, and refusing
-- it here would make completing two-factor authentication impossible.
--
-- password_changed_at is joined in rather than looked up separately so the
-- decision is made from one consistent read.
SELECT s.id, s.user_id, s.token_hash, s.created_at, s.last_seen_at,
       s.expires_at, s.authenticated_at, s.revoked_at,
       u.password_changed_at
  FROM sessions s
  JOIN users u ON u.id = s.user_id
 WHERE s.token_hash = $1;

-- name: RotateSessionToken :execrows
-- Replaces the token on a session and marks the second factor satisfied.
--
-- The token changes because the privilege does. A session that is handed a
-- cookie before authentication and keeps the same cookie afterwards is a
-- session fixation: anything that learned the pre-authentication token holds a
-- fully authenticated one the moment the person completes their second step.
--
-- The WHERE clause refuses to elevate a session twice, so a replayed
-- completion cannot mint a second live token for the same row.
UPDATE sessions
   SET token_hash = $2, authenticated_at = $3, last_seen_at = $3
 WHERE id = $1
   AND authenticated_at IS NULL
   AND revoked_at IS NULL
   AND expires_at > $3;

-- name: RevokeSession :execrows
-- Withdrawn, never deleted: see the column comment on sessions.revoked_at.
--
-- COALESCE rather than a `revoked_at IS NULL` clause, so that the row count
-- means one thing only. Filtering on it in the WHERE would make an
-- already-revoked session and a session that never existed both report zero
-- rows, and revoking twice would then be indistinguishable from revoking
-- nothing. Written this way the update is idempotent, the first revocation
-- time survives a second attempt, and zero rows means exactly what it says.
UPDATE sessions
   SET revoked_at = COALESCE(revoked_at, sqlc.arg(revoked_at))
 WHERE id = sqlc.arg(id);

-- name: RevokeUserSessions :execrows
-- Everything belonging to one person, optionally sparing one — which is what
-- "change my password and sign out my other devices" needs.
UPDATE sessions
   SET revoked_at = sqlc.arg(revoked_at)
 WHERE user_id = sqlc.arg(user_id)
   AND revoked_at IS NULL
   AND id <> sqlc.arg(except_id);

-- name: TouchSession :exec
-- Records that a session was used. Separate from the lookup because a read
-- should not have to be a write: the caller decides how often this is worth
-- doing, and an idle-timeout policy is what will decide it.
UPDATE sessions SET last_seen_at = $2 WHERE id = $1;

-- name: ListUserSessions :many
SELECT id, user_id, token_hash, created_at, last_seen_at,
       expires_at, authenticated_at, revoked_at
  FROM sessions
 WHERE user_id = $1
 ORDER BY created_at DESC, id DESC;

-- name: DeleteExpiredSessions :execrows
-- Sweeping. A revoked session is kept until it would have expired anyway, so
-- that "this session was revoked" remains answerable for as long as anyone
-- might ask.
DELETE FROM sessions WHERE expires_at <= $1;

-- ---------------------------------------------------------------------------
-- TOTP
-- ---------------------------------------------------------------------------

-- name: UpsertTOTPEnrolment :exec
-- Begins enrolment, replacing any unfinished one.
--
-- Re-enrolling resets the counter to zero, and that is safe precisely because
-- the secret is new: a counter only guards against a code being reused against
-- the secret it was generated from, and no code exists yet for this one.
INSERT INTO user_totp (user_id, secret, created_at)
VALUES ($1, $2, $3)
ON CONFLICT (user_id) DO UPDATE
    SET secret            = excluded.secret,
        created_at        = excluded.created_at,
        confirmed_at      = NULL,
        last_used_counter = 0;

-- name: GetTOTP :one
SELECT user_id, secret, confirmed_at, last_used_counter, created_at
  FROM user_totp WHERE user_id = $1;

-- name: GetTOTPForUpdate :one
-- The same reading, taking a row lock.
--
-- Verifying a code is read-decide-write: read the highest counter already
-- spent, check the code against the accepted window, write the counter it
-- matched. Two submissions of the same code running at once would otherwise
-- both read the old counter, both find the code unspent, and both succeed —
-- which is exactly the replay the counter exists to prevent, arriving through
-- the door left open by not holding the row. The lock makes the second one
-- wait, re-read, and correctly refuse.
SELECT user_id, secret, confirmed_at, last_used_counter, created_at
  FROM user_totp WHERE user_id = $1
   FOR UPDATE;

-- name: SetTOTPCounter :exec
UPDATE user_totp SET last_used_counter = $2 WHERE user_id = $1;

-- name: ConfirmTOTP :execrows
UPDATE user_totp
   SET confirmed_at = $2
 WHERE user_id = $1 AND confirmed_at IS NULL;

-- name: DeleteTOTP :execrows
DELETE FROM user_totp WHERE user_id = $1;

-- ---------------------------------------------------------------------------
-- Backup codes
-- ---------------------------------------------------------------------------

-- name: InsertBackupCode :exec
INSERT INTO user_backup_codes (user_id, code_hash, created_at)
VALUES ($1, $2, $3);

-- name: DeleteBackupCodes :execrows
-- Issuing a new set replaces the old one outright, spent codes included. A
-- code from a previous set must not survive a regeneration: the person asked
-- for a clean slate, usually because they think the old list leaked.
DELETE FROM user_backup_codes WHERE user_id = $1;

-- name: ListUnusedBackupCodeHashes :many
-- The candidates a submitted code is matched against.
--
-- No FOR UPDATE here, unlike the TOTP enrolment, and the difference is worth
-- being exact about because the two problems look identical and are not.
--
-- TOTP writes an unconditional `SET last_used_counter = $2`, so nothing in the
-- statement itself can tell that another transaction already advanced it. The
-- lock is what serialises those, and removing it lets two submissions of one
-- code both succeed — established by removing it and watching the concurrency
-- test go red.
--
-- Spending a backup code is naturally conditional instead: the row must still
-- be unspent, which MarkBackupCodeUsed states in its own WHERE. Under READ
-- COMMITTED the second UPDATE blocks on the first one's row lock, re-evaluates
-- its condition against the committed version, and matches nothing. A lock
-- taken here would add no guarantee — established the same way, by removing it
-- and watching the concurrency test stay green, which is why it is not here.
-- Do not add one back believing it protects something.
--
-- The `used_at IS NULL` filter is likewise not what makes a code single use; a
-- spent code that reached the matcher would still fail at the UPDATE. It is
-- here because it is the correct question and because it uses the partial
-- index. ORDER BY makes the result deterministic for the same reason every
-- other list in this file is ordered.
SELECT code_hash
  FROM user_backup_codes
 WHERE user_id = $1 AND used_at IS NULL
 ORDER BY code_hash;

-- name: MarkBackupCodeUsed :execrows
-- This is the whole of the single-use guarantee.
--
-- `used_at IS NULL` is not a defensive extra: it is the only thing standing
-- between one code and two uses of it. Two submissions racing here both reach
-- this statement, one updates a row and the other updates nothing. Remove the
-- clause and a backup code becomes reusable, which the concurrency test says
-- immediately.
UPDATE user_backup_codes
   SET used_at = $3
 WHERE user_id = $1 AND code_hash = $2 AND used_at IS NULL;

-- name: CountUnusedBackupCodes :one
SELECT count(*) FROM user_backup_codes WHERE user_id = $1 AND used_at IS NULL;
