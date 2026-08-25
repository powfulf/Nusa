-- Credentials, sessions and second factors.
--
-- Migration 2 created users with an identity and nothing else, and said in its
-- own comment that M2b would add credentials by ALTER rather than by creating a
-- second table and migrating rows into it. This is that ALTER.

-- ---------------------------------------------------------------------------
-- Credentials on the existing users table
-- ---------------------------------------------------------------------------

ALTER TABLE users
    ADD COLUMN email               text,
    ADD COLUMN password_hash       text,
    ADD COLUMN password_changed_at timestamptz;

COMMENT ON COLUMN users.email IS
    'Login identity, stored as typed. Uniqueness is case-insensitive; see users_email_key.';
COMMENT ON COLUMN users.password_hash IS
    'A PHC-format Argon2id string. The cost parameters travel inside it, so raising them does not invalidate existing credentials.';
COMMENT ON COLUMN users.password_changed_at IS
    'When the password was last set. Sessions created before it are no longer trusted.';

ALTER TABLE users
    -- A row with no credentials is not a half-finished user. It is a non-human
    -- actor — the rule engine, an importer, the AI layer — and M2a already
    -- required such rows to exist, because idempotency_keys.actor_id is NOT
    -- NULL and a replayed rule write has to be scoped to somebody. So the two
    -- columns travel together, and their joint absence is a meaningful state
    -- rather than an omission.
    ADD CONSTRAINT users_credentials_are_all_or_nothing
        CHECK ((email IS NULL) = (password_hash IS NULL)),

    ADD CONSTRAINT users_email_is_not_blank
        CHECK (email IS NULL OR btrim(email) <> ''),

    -- An address has to have a local part, an at sign and a domain. This is
    -- deliberately not an attempt at RFC 5322: a regex that tries to be exact
    -- about email rejects addresses that genuinely work, and the only real
    -- proof an address exists is sending to it. What this catches is a field
    -- that plainly holds something else.
    ADD CONSTRAINT users_email_looks_like_an_address
        CHECK (email IS NULL OR email ~ '^[^[:space:]@]+@[^[:space:]@]+\.[^[:space:]@]+$'),

    ADD CONSTRAINT users_password_hash_is_argon2id
        CHECK (password_hash IS NULL OR password_hash LIKE '$argon2id$v=%'),

    ADD CONSTRAINT users_password_changed_with_the_password
        CHECK ((password_hash IS NULL) = (password_changed_at IS NULL));

-- Case-insensitive uniqueness, over the address as typed.
--
-- Storing the address lowercased instead would be simpler and would lose what
-- the person wrote, which appears back to them in the interface and in a TOTP
-- provisioning label. Nobody's address is improved by being flattened, and the
-- comparison is the only place the case is irrelevant — so the normalisation
-- lives in the index rather than in the column.
CREATE UNIQUE INDEX users_email_key ON users (lower(email)) WHERE email IS NOT NULL;

-- At most one credentialed user, enforced by the database.
--
-- The product rule is that the first person to arrive at a fresh instance may
-- register, and registration is closed after that until M9 brings households
-- and invitations. Two simultaneous registrations against an empty instance
-- must produce one user, not two.
--
-- A SELECT-then-INSERT cannot promise that: under READ COMMITTED both
-- transactions see an empty table and both proceed. Rather than add a lock
-- that every future caller has to remember to take, the rule is expressed as
-- something the database cannot violate. The index is over a constant
-- expression restricted to credentialed rows, so every such row indexes the
-- same key and the second insert collides with the first.
--
-- The collision is also deterministic rather than lucky: a second inserter
-- blocks on the uncommitted index entry until the first transaction resolves,
-- then fails. That is what makes it testable without racing goroutines.
--
-- M9 drops this index. Until then it is the whole of the registration policy,
-- and it is one line.
CREATE UNIQUE INDEX users_at_most_one_credentialed
    ON users ((email IS NOT NULL)) WHERE email IS NOT NULL;

-- ---------------------------------------------------------------------------
-- Sessions
-- ---------------------------------------------------------------------------

-- Server-side and revocable, per section 10. There are no JWTs, and the reason
-- is exactly this table: a token that carries its own claims cannot be
-- withdrawn before it expires, and "log out everywhere" then means "wait".
CREATE TABLE sessions (
    id      uuid NOT NULL PRIMARY KEY,
    user_id uuid NOT NULL REFERENCES users (id) ON DELETE RESTRICT,

    -- SHA-256 of the token in the cookie, never the token itself.
    --
    -- The token is 256 bits drawn from a cryptographic source, so there is no
    -- distribution to enumerate and a fast hash is right here for the same
    -- reason it is right for a backup code. What it buys is that a leaked
    -- database yields no usable cookie: an attacker holding these rows still
    -- cannot present a session.
    --
    -- It is also why the identity and the credential are two different values.
    -- id appears in logs and in the audit trail; token_hash never leaves this
    -- column, and the token itself exists only in the response that set it.
    token_hash bytea NOT NULL,

    created_at   timestamptz NOT NULL DEFAULT now(),
    last_seen_at timestamptz NOT NULL DEFAULT now(),
    expires_at   timestamptz NOT NULL,

    -- When the second factor was satisfied. NULL means the password has been
    -- accepted and nothing else has: the session exists, and may do nothing
    -- but complete or abandon its second step.
    --
    -- A session with 2FA switched off is authenticated the moment it is
    -- created, so this equals created_at rather than staying NULL. "Not yet
    -- authenticated" and "no second factor configured" are different states
    -- and a single nullable column would conflate them.
    authenticated_at timestamptz,

    -- Withdrawn, rather than deleted. A deleted row cannot tell "revoked" from
    -- "never existed", and the first of those is something the audit trail and
    -- the person's own session list are entitled to show.
    revoked_at timestamptz,

    CONSTRAINT sessions_id_is_uuid_v7 CHECK (is_uuid_v7(id)),
    CONSTRAINT sessions_token_hash_is_sha256 CHECK (octet_length(token_hash) = 32),
    CONSTRAINT sessions_expire_after_they_are_created CHECK (expires_at > created_at),
    CONSTRAINT sessions_are_authenticated_after_they_are_created
        CHECK (authenticated_at IS NULL OR authenticated_at >= created_at),
    CONSTRAINT sessions_are_revoked_after_they_are_created
        CHECK (revoked_at IS NULL OR revoked_at >= created_at),
    CONSTRAINT sessions_are_seen_after_they_are_created
        CHECK (last_seen_at >= created_at)
);

COMMENT ON TABLE sessions IS
    'Server-side sessions. The cookie carries a random token; this table stores only its SHA-256.';

-- The lookup every authenticated request performs, and the reason token_hash
-- is unique: two sessions sharing a token would make the credential ambiguous.
CREATE UNIQUE INDEX sessions_token_hash_key ON sessions (token_hash);

-- Revoking everything belonging to one person, and showing them their own
-- sessions. Newest first, because that is the order both want.
CREATE INDEX sessions_user_idx ON sessions (user_id, created_at DESC);

-- Sweeping what has expired.
CREATE INDEX sessions_expires_at_idx ON sessions (expires_at);

-- ---------------------------------------------------------------------------
-- Second factor: TOTP
-- ---------------------------------------------------------------------------

CREATE TABLE user_totp (
    user_id uuid NOT NULL PRIMARY KEY REFERENCES users (id) ON DELETE RESTRICT,

    -- The shared secret, as bytes. Stored in clear, and that is a decision
    -- rather than an oversight: encrypting it would need a key, the key would
    -- live in the same .env beside the same backup as the database, and an
    -- attacker holding one would hold the other. It would buy the appearance
    -- of protection and no protection. Encryption under an operator-supplied
    -- key that is genuinely kept elsewhere is a real improvement and is
    -- deferred rather than faked.
    --
    -- The secret alone is not enough to sign in: a second factor is second.
    secret bytea NOT NULL,

    -- NULL while enrolment is in progress. A secret is generated and shown as
    -- a QR code before the person has proved they can produce a code from it,
    -- and until they do, two-factor authentication is not on. Storing the
    -- unconfirmed secret in the same row is what lets a half-finished
    -- enrolment be resumed or abandoned without a second table.
    confirmed_at timestamptz,

    -- The highest counter a code has been accepted at.
    --
    -- Clock skew makes one code acceptable for 60 to 90 seconds, so without
    -- this a code observed in transit could be presented a second time and
    -- would be accepted. Every successful verification advances it, inside the
    -- same transaction that read it.
    last_used_counter bigint NOT NULL DEFAULT 0,

    created_at timestamptz NOT NULL DEFAULT now(),

    -- RFC 4226 sets 128 bits as the floor for a shared secret.
    CONSTRAINT user_totp_secret_is_long_enough CHECK (octet_length(secret) >= 16),
    CONSTRAINT user_totp_secret_is_not_absurd CHECK (octet_length(secret) <= 128),
    CONSTRAINT user_totp_counter_is_not_negative CHECK (last_used_counter >= 0),
    CONSTRAINT user_totp_confirmed_after_it_was_created
        CHECK (confirmed_at IS NULL OR confirmed_at >= created_at),
    -- A counter can only have advanced if a code was ever accepted, and a code
    -- can only be accepted once enrolment is confirmed.
    CONSTRAINT user_totp_unconfirmed_has_no_counter
        CHECK (confirmed_at IS NOT NULL OR last_used_counter = 0)
);

COMMENT ON TABLE user_totp IS
    'One TOTP enrolment per user. Unconfirmed rows are enrolments in progress, not active second factors.';

-- ---------------------------------------------------------------------------
-- Second factor: backup codes
-- ---------------------------------------------------------------------------

CREATE TABLE user_backup_codes (
    user_id uuid NOT NULL REFERENCES users (id) ON DELETE RESTRICT,

    -- Lowercase hex of the SHA-256 of the normalised code. Argon2id is not
    -- used here and the reasoning is in internal/auth/backupcode.go: a code is
    -- drawn rather than chosen, at 80 bits, so there is no distribution for a
    -- slow hash to defend against — and checking one submission would
    -- otherwise mean ten memory-hard hashes.
    code_hash text NOT NULL,

    created_at timestamptz NOT NULL DEFAULT now(),

    -- Spent, rather than deleted. A person is owed an answer to "have my
    -- backup codes been used, and when", and a deleted row cannot give it.
    -- Whether a code is available is `used_at IS NULL`, which is also what
    -- makes spending a code an UPDATE that either affects one row or none.
    used_at timestamptz,

    PRIMARY KEY (user_id, code_hash),

    CONSTRAINT user_backup_codes_hash_is_sha256_hex
        CHECK (code_hash ~ '^[0-9a-f]{64}$'),
    CONSTRAINT user_backup_codes_used_after_they_were_created
        CHECK (used_at IS NULL OR used_at >= created_at)
);

COMMENT ON TABLE user_backup_codes IS
    'Single-use recovery codes, stored as SHA-256 hex. A spent code keeps its row so it can be reported.';

-- Reading the codes a user still holds, which is both the verification path
-- and the "how many are left" question the interface asks.
CREATE INDEX user_backup_codes_unused_idx
    ON user_backup_codes (user_id) WHERE used_at IS NULL;

UPDATE schema_meta SET version = 8, applied_at = now();
