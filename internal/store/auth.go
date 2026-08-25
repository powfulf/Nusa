// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/GaffaQ/Nusa/internal/auth"
	"github.com/GaffaQ/Nusa/internal/ledger"
)

// The storage behind internal/auth.
//
// The dependency runs one way: this file imports auth, and auth imports
// nothing of this. Two of the methods here take an auth.Authenticator and a
// submitted code rather than returning a secret for the caller to check, and
// that is what atomicity requires rather than a layering slip — the policy is
// still entirely inside Authenticator.Validate. It is the same arrangement
// SaveDisposal uses, where the store holds the lots under a row lock and calls
// ledger.ConsumeFIFO between the read and the write.

// Store embeds *Queries, so a generated query is normally reached as s.X.
// Three methods below share a name with the query they wrap — RevokeSession,
// RevokeUserSessions and TouchSession — and there the outer method wins, so
// those three are written s.Queries.X. Unqualified they call themselves, and
// what catches it today is only that the signatures differ. A future method
// wrapping a same-named query with a matching signature would recurse in
// silence, so check for that before choosing a name.

// Compile-time proof that this type satisfies what auth asks for. Without
// these, a signature could drift apart from its interface and nothing would
// notice until whichever handler in M2b's later steps tried to use it.
var (
	_ auth.CredentialStore   = (*Store)(nil)
	_ auth.SessionStore      = (*Store)(nil)
	_ auth.SecondFactorStore = (*Store)(nil)
)

// inTx runs fn inside one database transaction, rolling back on any error.
//
// The rollback is deferred unconditionally: after a successful Commit it is a
// no-op, and on any path that returns early it is the only thing that releases
// the row locks these operations take.
func (s *Store) inTx(ctx context.Context, fn func(q *Queries) error) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := fn(s.WithTx(tx)); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Credentials
// ---------------------------------------------------------------------------

// Register creates an account.
//
// Whether registration is open is decided by the database, not here. A
// SELECT-then-INSERT cannot promise that two simultaneous registrations
// against an empty instance produce one account, so the rule lives in the
// users_at_most_one_credentialed index and this method's job is to translate
// the unique violation into something a caller can act on.
func (s *Store) Register(ctx context.Context, r auth.Registration) error {
	if err := ledger.ValidateID(r.UserID); err != nil {
		return fmt.Errorf("%w: user id: %w", ErrInvalidWrite, err)
	}
	if r.Email == "" || r.PasswordHash == "" {
		return fmt.Errorf("%w: registration needs an email and a password hash", ErrInvalidWrite)
	}
	if err := nulNotAllowed("email", r.Email); err != nil {
		return err
	}
	if err := nulNotAllowed("password hash", r.PasswordHash); err != nil {
		return err
	}

	id, err := uuidFrom(r.UserID)
	if err != nil {
		return err
	}

	at := timestampFrom(r.At)
	err = s.InsertCredentialedUser(ctx, InsertCredentialedUserParams{
		ID:                id,
		Email:             &r.Email,
		PasswordHash:      &r.PasswordHash,
		PasswordChangedAt: at,
		CreatedAt:         at,
	})
	switch {
	case constraintNamed(err, "23505", "users_at_most_one_credentialed"):
		return auth.ErrRegistrationClosed
	case constraintNamed(err, "23505", "users_email_key"):
		return auth.ErrEmailTaken
	case constraintNamed(err, "23505", "users_pkey"):
		return fmt.Errorf("%w: user id %s already exists", ErrInvalidWrite, r.UserID)
	case err != nil:
		return fmt.Errorf("insert user: %w", err)
	}
	return nil
}

// CredentialsByEmail looks an account up case-insensitively.
func (s *Store) CredentialsByEmail(ctx context.Context, email string) (auth.Credentials, error) {
	row, err := s.GetCredentialsByEmail(ctx, email)
	if errors.Is(err, pgx.ErrNoRows) {
		return auth.Credentials{}, auth.ErrNoSuchUser
	}
	if err != nil {
		return auth.Credentials{}, fmt.Errorf("get credentials by email: %w", err)
	}
	return credentialsFrom(row.ID, row.Email, row.PasswordHash, row.PasswordChangedAt, row.CreatedAt)
}

// CredentialsByID looks an account up by identity.
//
// A row with no credentials is reported as ErrNoSuchUser rather than as an
// empty Credentials. Such rows exist and are meaningful — they are the
// non-human actors audit_log and idempotency_keys point at — but for the
// purpose of signing somebody in, an actor that cannot hold a password is not
// a user, and returning a blank hash would invite something to compare against
// it.
func (s *Store) CredentialsByID(ctx context.Context, userID string) (auth.Credentials, error) {
	id, err := uuidFrom(userID)
	if err != nil {
		return auth.Credentials{}, err
	}
	row, err := s.GetCredentialsByID(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return auth.Credentials{}, auth.ErrNoSuchUser
	}
	if err != nil {
		return auth.Credentials{}, fmt.Errorf("get credentials by id: %w", err)
	}
	return credentialsFrom(row.ID, row.Email, row.PasswordHash, row.PasswordChangedAt, row.CreatedAt)
}

func credentialsFrom(
	id pgtype.UUID, email, hash *string, changedAt, createdAt pgtype.Timestamptz,
) (auth.Credentials, error) {
	if email == nil || hash == nil {
		return auth.Credentials{}, auth.ErrNoSuchUser
	}
	return auth.Credentials{
		UserID:            uuidTo(id),
		Email:             *email,
		PasswordHash:      *hash,
		PasswordChangedAt: timestampTo(changedAt),
		CreatedAt:         timestampTo(createdAt),
	}, nil
}

// SetPasswordHash replaces a password. It does not touch sessions.
func (s *Store) SetPasswordHash(ctx context.Context, userID, hash string, at time.Time) error {
	if hash == "" {
		return fmt.Errorf("%w: empty password hash", ErrInvalidWrite)
	}
	if err := nulNotAllowed("password hash", hash); err != nil {
		return err
	}
	id, err := uuidFrom(userID)
	if err != nil {
		return err
	}

	rows, err := s.UpdatePasswordHash(ctx, UpdatePasswordHashParams{
		ID:                id,
		PasswordHash:      &hash,
		PasswordChangedAt: timestampFrom(at),
	})
	if err != nil {
		return fmt.Errorf("update password hash: %w", err)
	}
	if rows == 0 {
		return auth.ErrNoSuchUser
	}
	return nil
}

// HasCredentialedUser reports whether anybody has registered.
func (s *Store) HasCredentialedUser(ctx context.Context) (bool, error) {
	n, err := s.CountCredentialedUsers(ctx)
	if err != nil {
		return false, fmt.Errorf("count credentialed users: %w", err)
	}
	return n > 0, nil
}

// ---------------------------------------------------------------------------
// Sessions
// ---------------------------------------------------------------------------

// CreateSession records a sign-in.
func (s *Store) CreateSession(ctx context.Context, ns auth.NewSession) error {
	if err := ledger.ValidateID(ns.ID); err != nil {
		return fmt.Errorf("%w: session id: %w", ErrInvalidWrite, err)
	}
	if err := ledger.ValidateID(ns.UserID); err != nil {
		return fmt.Errorf("%w: user id: %w", ErrInvalidWrite, err)
	}
	if ns.Token == "" {
		return fmt.Errorf("%w: session token is empty", ErrInvalidWrite)
	}
	if !ns.ExpiresAt.After(ns.CreatedAt) {
		return fmt.Errorf("%w: session expires at or before it is created", ErrInvalidWrite)
	}

	id, err := uuidFrom(ns.ID)
	if err != nil {
		return err
	}
	userID, err := uuidFrom(ns.UserID)
	if err != nil {
		return err
	}

	// A session with nothing left to prove is authenticated as of its
	// creation. Leaving the column NULL instead would make "no second factor
	// configured" indistinguishable from "second factor still pending", and
	// the first of those may act while the second may not.
	var authenticatedAt pgtype.Timestamptz
	if ns.Authenticated {
		authenticatedAt = timestampFrom(ns.CreatedAt)
	}

	err = s.InsertSession(ctx, InsertSessionParams{
		ID:              id,
		UserID:          userID,
		TokenHash:       auth.HashSessionToken(ns.Token),
		CreatedAt:       timestampFrom(ns.CreatedAt),
		ExpiresAt:       timestampFrom(ns.ExpiresAt),
		AuthenticatedAt: authenticatedAt,
	})
	if err != nil {
		return fmt.Errorf("insert session: %w", err)
	}
	return nil
}

// SessionByToken resolves a cookie value to a session that may still be acted
// on.
//
// A session awaiting its second factor is returned rather than refused: it is
// the credential the second step itself presents, and refusing it here would
// make completing two-factor authentication impossible. The caller reads
// Session.PendingSecondFactor and allows only that step.
//
// The three ways of being finished are decided here rather than by the caller.
// Expiry, revocation and a password changed since the session began are one
// answer to whoever is holding the cookie, and a caller obliged to remember
// all three will eventually remember two.
func (s *Store) SessionByToken(ctx context.Context, token string, now time.Time) (auth.Session, error) {
	if token == "" {
		return auth.Session{}, auth.ErrNoSession
	}

	row, err := s.GetSessionByTokenHash(ctx, auth.HashSessionToken(token))
	if errors.Is(err, pgx.ErrNoRows) {
		return auth.Session{}, auth.ErrNoSession
	}
	if err != nil {
		return auth.Session{}, fmt.Errorf("get session: %w", err)
	}

	session := auth.Session{
		ID:              uuidTo(row.ID),
		UserID:          uuidTo(row.UserID),
		CreatedAt:       timestampTo(row.CreatedAt),
		LastSeenAt:      timestampTo(row.LastSeenAt),
		ExpiresAt:       timestampTo(row.ExpiresAt),
		AuthenticatedAt: timestampTo(row.AuthenticatedAt),
		RevokedAt:       timestampTo(row.RevokedAt),
	}

	switch {
	case !session.RevokedAt.IsZero():
		return auth.Session{}, fmt.Errorf("%w: revoked at %s",
			auth.ErrSessionNotUsable, session.RevokedAt.UTC().Format(time.RFC3339))
	case !session.ExpiresAt.After(now):
		return auth.Session{}, fmt.Errorf("%w: expired at %s",
			auth.ErrSessionNotUsable, session.ExpiresAt.UTC().Format(time.RFC3339))
	}

	// A password change ends every session that predates it, without those
	// sessions having to be found and rewritten. Revoking them explicitly is
	// also offered, and is what a caller uses when it wants the rows to say so;
	// this is the backstop that holds even if that call is missed.
	if changed := timestampTo(row.PasswordChangedAt); !changed.IsZero() && changed.After(session.CreatedAt) {
		return auth.Session{}, fmt.Errorf("%w: password changed at %s",
			auth.ErrSessionNotUsable, changed.UTC().Format(time.RFC3339))
	}

	return session, nil
}

// ElevateSession marks the second factor satisfied and swaps in a new token.
func (s *Store) ElevateSession(ctx context.Context, sessionID, newToken string, at time.Time) error {
	if newToken == "" {
		return fmt.Errorf("%w: replacement session token is empty", ErrInvalidWrite)
	}
	id, err := uuidFrom(sessionID)
	if err != nil {
		return err
	}

	rows, err := s.RotateSessionToken(ctx, RotateSessionTokenParams{
		ID:              id,
		TokenHash:       auth.HashSessionToken(newToken),
		AuthenticatedAt: timestampFrom(at),
	})
	if err != nil {
		return fmt.Errorf("rotate session token: %w", err)
	}
	if rows == 0 {
		// The statement refuses a session that is already elevated, revoked or
		// expired. All three mean the same thing to the caller: this session
		// is not one that can complete a second step now.
		return auth.ErrSessionNotUsable
	}
	return nil
}

// RevokeSession withdraws one session.
func (s *Store) RevokeSession(ctx context.Context, sessionID string, at time.Time) error {
	id, err := uuidFrom(sessionID)
	if err != nil {
		return err
	}
	rows, err := s.Queries.RevokeSession(ctx, RevokeSessionParams{ID: id, RevokedAt: timestampFrom(at)})
	if err != nil {
		return fmt.Errorf("revoke session: %w", err)
	}
	if rows == 0 {
		// Zero rows means no such session, and nothing else: the statement
		// matches on identity alone and leaves an existing revocation time
		// untouched, so revoking twice affects a row and reports success.
		// That is the right answer — the caller wanted the session gone and it
		// is — while a session that never existed is still worth reporting.
		return auth.ErrNoSession
	}
	return nil
}

// RevokeUserSessions withdraws every live session belonging to userID except
// exceptID, and reports how many it withdrew.
func (s *Store) RevokeUserSessions(
	ctx context.Context, userID, exceptID string, at time.Time,
) (int64, error) {
	user, err := uuidFrom(userID)
	if err != nil {
		return 0, err
	}
	// An empty exception is a valid request meaning "spare nothing". uuidFrom
	// renders that as a NULL uuid, and `id <> NULL` is never true, so the row
	// would be spared rather than revoked. A zero uuid matches no session,
	// which is what "spare nothing" has to compile to.
	except := pgtype.UUID{Valid: true}
	if exceptID != "" {
		if except, err = uuidFrom(exceptID); err != nil {
			return 0, err
		}
	}

	rows, err := s.Queries.RevokeUserSessions(ctx, RevokeUserSessionsParams{
		UserID:    user,
		ExceptID:  except,
		RevokedAt: timestampFrom(at),
	})
	if err != nil {
		return 0, fmt.Errorf("revoke user sessions: %w", err)
	}
	return rows, nil
}

// TouchSession records that a session was used.
func (s *Store) TouchSession(ctx context.Context, sessionID string, at time.Time) error {
	id, err := uuidFrom(sessionID)
	if err != nil {
		return err
	}
	if err := s.Queries.TouchSession(ctx, TouchSessionParams{ID: id, LastSeenAt: timestampFrom(at)}); err != nil {
		return fmt.Errorf("touch session: %w", err)
	}
	return nil
}

// UserSessions lists a person's sessions, newest first, revoked ones included
// so they can be shown as such.
func (s *Store) UserSessions(ctx context.Context, userID string) ([]auth.Session, error) {
	id, err := uuidFrom(userID)
	if err != nil {
		return nil, err
	}
	rows, err := s.ListUserSessions(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("list user sessions: %w", err)
	}

	sessions := make([]auth.Session, 0, len(rows))
	for _, row := range rows {
		sessions = append(sessions, auth.Session{
			ID:              uuidTo(row.ID),
			UserID:          uuidTo(row.UserID),
			CreatedAt:       timestampTo(row.CreatedAt),
			LastSeenAt:      timestampTo(row.LastSeenAt),
			ExpiresAt:       timestampTo(row.ExpiresAt),
			AuthenticatedAt: timestampTo(row.AuthenticatedAt),
			RevokedAt:       timestampTo(row.RevokedAt),
		})
	}
	return sessions, nil
}

// SweepExpiredSessions deletes sessions past their expiry and reports how many
// went.
func (s *Store) SweepExpiredSessions(ctx context.Context, asOf time.Time) (int64, error) {
	rows, err := s.DeleteExpiredSessions(ctx, timestampFrom(asOf))
	if err != nil {
		return 0, fmt.Errorf("sweep expired sessions: %w", err)
	}
	return rows, nil
}

// ---------------------------------------------------------------------------
// TOTP
// ---------------------------------------------------------------------------

// BeginTOTPEnrolment stores a secret that has not been proved yet.
func (s *Store) BeginTOTPEnrolment(ctx context.Context, userID string, secret []byte, at time.Time) error {
	id, err := uuidFrom(userID)
	if err != nil {
		return err
	}
	if len(secret) < 16 {
		return fmt.Errorf("%w: totp secret is %d bytes, RFC 4226 asks for at least 16",
			ErrInvalidWrite, len(secret))
	}

	err = s.UpsertTOTPEnrolment(ctx, UpsertTOTPEnrolmentParams{
		UserID:    id,
		Secret:    secret,
		CreatedAt: timestampFrom(at),
	})
	if err != nil {
		return fmt.Errorf("upsert totp enrolment: %w", err)
	}
	return nil
}

// TOTPEnrolment reads an enrolment, confirmed or not.
func (s *Store) TOTPEnrolment(ctx context.Context, userID string) (auth.TOTPEnrolment, error) {
	id, err := uuidFrom(userID)
	if err != nil {
		return auth.TOTPEnrolment{}, err
	}
	row, err := s.GetTOTP(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return auth.TOTPEnrolment{}, auth.ErrNoTOTPEnrolment
	}
	if err != nil {
		return auth.TOTPEnrolment{}, fmt.Errorf("get totp: %w", err)
	}
	return enrolmentTo(row)
}

func enrolmentTo(row UserTotp) (auth.TOTPEnrolment, error) {
	if row.LastUsedCounter < 0 {
		// The column has a CHECK forbidding this, so reaching it means the row
		// was written by something that bypassed the constraint. Refused
		// rather than converted, because a negative read as unsigned becomes
		// an enormous counter that would reject every future code.
		return auth.TOTPEnrolment{}, fmt.Errorf("%w: negative totp counter %d",
			ErrInvalidWrite, row.LastUsedCounter)
	}
	return auth.TOTPEnrolment{
		UserID:          uuidTo(row.UserID),
		Secret:          row.Secret,
		ConfirmedAt:     timestampTo(row.ConfirmedAt),
		LastUsedCounter: uint64(row.LastUsedCounter),
		CreatedAt:       timestampTo(row.CreatedAt),
	}, nil
}

// counterToColumn narrows a TOTP counter for a bigint column.
//
// A counter is periods since 1970, so it passes 2^63 only for instants far
// beyond any clock this will run against. It is checked anyway, because the
// alternative to an error here is a negative value in the column and a
// constraint violation naming neither the cause nor the fix.
func counterToColumn(counter uint64) (int64, error) {
	if counter > math.MaxInt64 {
		return 0, fmt.Errorf("%w: totp counter %d does not fit a bigint", ErrInvalidWrite, counter)
	}
	return int64(counter), nil
}

// ConfirmTOTPEnrolment finishes enrolment by proving a code against the stored
// secret.
//
// The counter that proved it is spent in the same transaction. Without that,
// the very code someone typed to finish enrolling would still be acceptable a
// moment later as a login — the enrolment step would hand an eavesdropper a
// usable second factor.
func (s *Store) ConfirmTOTPEnrolment(
	ctx context.Context, a auth.Authenticator, userID, code string, at time.Time,
) error {
	id, err := uuidFrom(userID)
	if err != nil {
		return err
	}

	return s.inTx(ctx, func(q *Queries) error {
		row, err := q.GetTOTPForUpdate(ctx, id)
		if errors.Is(err, pgx.ErrNoRows) {
			return auth.ErrNoTOTPEnrolment
		}
		if err != nil {
			return fmt.Errorf("lock totp enrolment: %w", err)
		}
		if timestampTo(row.ConfirmedAt) != (time.Time{}) {
			return auth.ErrTOTPAlreadyConfirmed
		}

		counter, err := a.Validate(row.Secret, code, at, 0)
		if err != nil {
			return err
		}
		spent, err := counterToColumn(counter)
		if err != nil {
			return err
		}

		// Confirm before spending the counter, not after. The
		// user_totp_unconfirmed_has_no_counter constraint is checked per
		// statement rather than at commit, so writing the counter onto a row
		// that is still unconfirmed fails — even though both statements are in
		// one transaction and the end state is legal. Found by the constraint
		// itself, which is what it was written for.
		rows, err := q.ConfirmTOTP(ctx, ConfirmTOTPParams{UserID: id, ConfirmedAt: timestampFrom(at)})
		if err != nil {
			return fmt.Errorf("confirm totp: %w", err)
		}
		if rows == 0 {
			return auth.ErrTOTPAlreadyConfirmed
		}
		if err := q.SetTOTPCounter(ctx, SetTOTPCounterParams{UserID: id, LastUsedCounter: spent}); err != nil {
			return fmt.Errorf("set totp counter: %w", err)
		}
		return nil
	})
}

// ConsumeTOTPCode verifies a code and spends the counter it matched.
//
// Read, decide and write all happen inside one transaction holding the
// enrolment row. Clock skew makes a single code acceptable for 60 to 90
// seconds, so two submissions of it can genuinely arrive at once; without the
// lock both would read the same counter, both find the code unspent, and both
// succeed — which is the replay the counter exists to prevent, arriving
// through the door left open by deciding outside the transaction.
func (s *Store) ConsumeTOTPCode(
	ctx context.Context, a auth.Authenticator, userID, code string, at time.Time,
) error {
	id, err := uuidFrom(userID)
	if err != nil {
		return err
	}

	return s.inTx(ctx, func(q *Queries) error {
		row, err := q.GetTOTPForUpdate(ctx, id)
		if errors.Is(err, pgx.ErrNoRows) {
			return auth.ErrNoTOTPEnrolment
		}
		if err != nil {
			return fmt.Errorf("lock totp enrolment: %w", err)
		}

		enrolment, err := enrolmentTo(row)
		if err != nil {
			return err
		}
		if !enrolment.Confirmed() {
			return auth.ErrTOTPNotConfirmed
		}

		counter, err := a.Validate(enrolment.Secret, code, at, enrolment.LastUsedCounter)
		if err != nil {
			return err
		}
		spent, err := counterToColumn(counter)
		if err != nil {
			return err
		}

		if err := q.SetTOTPCounter(ctx, SetTOTPCounterParams{UserID: id, LastUsedCounter: spent}); err != nil {
			return fmt.Errorf("set totp counter: %w", err)
		}
		return nil
	})
}

// DisableTOTP removes an enrolment.
func (s *Store) DisableTOTP(ctx context.Context, userID string) error {
	id, err := uuidFrom(userID)
	if err != nil {
		return err
	}
	rows, err := s.DeleteTOTP(ctx, id)
	if err != nil {
		return fmt.Errorf("delete totp: %w", err)
	}
	if rows == 0 {
		return auth.ErrNoTOTPEnrolment
	}
	return nil
}

// ---------------------------------------------------------------------------
// Backup codes
// ---------------------------------------------------------------------------

// ReplaceBackupCodes issues a new set, discarding the previous one entirely.
//
// It takes the codes in clear and hashes them here, so that no caller ever
// holds a stored form and no caller can store something that is not a hash.
func (s *Store) ReplaceBackupCodes(
	ctx context.Context, userID string, codes []string, at time.Time,
) error {
	id, err := uuidFrom(userID)
	if err != nil {
		return err
	}
	if len(codes) == 0 {
		return fmt.Errorf("%w: no backup codes to store", ErrInvalidWrite)
	}

	return s.inTx(ctx, func(q *Queries) error {
		if _, err := q.DeleteBackupCodes(ctx, id); err != nil {
			return fmt.Errorf("delete backup codes: %w", err)
		}
		for _, code := range codes {
			err := q.InsertBackupCode(ctx, InsertBackupCodeParams{
				UserID:    id,
				CodeHash:  auth.HashBackupCode(code),
				CreatedAt: timestampFrom(at),
			})
			if err != nil {
				return fmt.Errorf("insert backup code: %w", err)
			}
		}
		return nil
	})
}

// ConsumeBackupCode spends a code.
//
// The transaction here is for atomicity of the pair of statements, not for a
// lock. What makes a code single use is the `used_at IS NULL` clause on the
// UPDATE: two submissions of one code both reach it, and one of them updates
// nothing. That differs from ConsumeTOTPCode, whose write is unconditional and
// therefore does need the row held — see the comment on
// ListUnusedBackupCodeHashes, which records how each of the two was
// established rather than assumed.
func (s *Store) ConsumeBackupCode(ctx context.Context, userID, code string, at time.Time) error {
	id, err := uuidFrom(userID)
	if err != nil {
		return err
	}

	return s.inTx(ctx, func(q *Queries) error {
		hashes, err := q.ListUnusedBackupCodeHashes(ctx, id)
		if err != nil {
			return fmt.Errorf("list backup codes: %w", err)
		}

		index, err := auth.MatchBackupCode(hashes, code)
		if err != nil {
			return err
		}

		rows, err := q.MarkBackupCodeUsed(ctx, MarkBackupCodeUsedParams{
			UserID:   id,
			CodeHash: hashes[index],
			UsedAt:   timestampFrom(at),
		})
		if err != nil {
			return fmt.Errorf("mark backup code used: %w", err)
		}
		if rows == 0 {
			// Reachable, and the path that matters: another submission of this
			// same code spent it between the list above and this update. That
			// is not an error in the ordinary sense — the code was valid — but
			// it was already used, and this caller must be told the same thing
			// as anyone presenting a code that never existed.
			return auth.ErrInvalidBackupCode
		}
		return nil
	})
}

// UnusedBackupCodeCount reports how many codes a person has left.
func (s *Store) UnusedBackupCodeCount(ctx context.Context, userID string) (int, error) {
	id, err := uuidFrom(userID)
	if err != nil {
		return 0, err
	}
	n, err := s.CountUnusedBackupCodes(ctx, id)
	if err != nil {
		return 0, fmt.Errorf("count unused backup codes: %w", err)
	}
	return int(n), nil
}

// RecordEvent writes one authentication event to the audit log.
//
// Unlike a ledger mutation this is not inside a caller's transaction, and it
// does not need to be: there is no accompanying write it could commit apart
// from. A ledger audit entry is bound to its change because an entry that
// committed separately would eventually describe a change that did not happen;
// a sign-in has no such pair.
//
// The origin is always human. Nothing else signs in — a rule or an importer
// acts under an actor that was authenticated once by a person, and its writes
// are audited where they happen rather than here.
func (s *Store) RecordEvent(ctx context.Context, e auth.Event) error {
	if err := ledger.ValidateID(e.ID); err != nil {
		return fmt.Errorf("%w: event id: %w", ErrInvalidWrite, err)
	}
	if err := ledger.ValidateID(e.ActorID); err != nil {
		return fmt.Errorf("%w: event actor: %w", ErrInvalidWrite, err)
	}
	if e.Action == "" || e.EntityKind == "" {
		return fmt.Errorf("%w: an event needs an action and an entity kind", ErrInvalidWrite)
	}

	id, err := uuidFrom(e.ID)
	if err != nil {
		return err
	}
	actor, err := uuidFrom(e.ActorID)
	if err != nil {
		return err
	}
	entity, err := uuidFrom(e.EntityID)
	if err != nil {
		return err
	}

	detail := e.Detail
	if len(detail) == 0 {
		detail = []byte("{}")
	}

	err = s.InsertAuditEntry(ctx, InsertAuditEntryParams{
		ID:         id,
		OccurredAt: timestampFrom(e.At),
		ActorID:    actor,
		Origin:     string(OriginHuman),
		Action:     e.Action,
		EntityKind: e.EntityKind,
		EntityID:   entity,
		Diff:       detail,
	})
	if err != nil {
		return fmt.Errorf("record auth event %s: %w", e.Action, err)
	}
	return nil
}
