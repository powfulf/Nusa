// SPDX-License-Identifier: AGPL-3.0-only

package auth

import (
	"context"
	"time"
)

// The storage this package needs, described as interfaces here and implemented
// in internal/store.
//
// They are split by concern rather than gathered into one Repository because
// the things that will use them are also split: signing in needs credentials
// and sessions and knows nothing about enrolment, while a settings page that
// regenerates backup codes needs none of the login path. A handler that can
// only reach what it needs is a handler that cannot accidentally reach
// anything else.
//
// Two of these methods take an Authenticator and a code rather than returning a
// secret for the caller to check. That is not a layering slip — the policy
// still lives entirely in Authenticator.Validate — it is what atomicity
// requires. Verifying a code is read-decide-write against a counter, and if the
// decision happens outside the transaction that read the counter, two
// submissions of the same code can both succeed. The same arrangement the
// ledger uses for a disposal, where the store holds the lots and calls
// ledger.ConsumeFIFO between the read and the write.

// Credentials are what a sign-in is checked against.
type Credentials struct {
	// UserID is the person's identity, a canonical lowercase UUIDv7.
	UserID string

	// Email is the address as they typed it. Uniqueness is case-insensitive;
	// this is not folded.
	Email string

	// PasswordHash is a PHC-format Argon2id string. Pass it to Verify.
	PasswordHash string

	// PasswordChangedAt is when the password was last set. Sessions that
	// predate it are no longer trusted, which is what makes changing a
	// password end other sign-ins.
	PasswordChangedAt time.Time

	// CreatedAt is when the account was registered.
	CreatedAt time.Time
}

// Registration is a new account.
type Registration struct {
	UserID       string
	Email        string
	PasswordHash string
	At           time.Time
}

// NewSession is a sign-in about to be recorded.
type NewSession struct {
	// ID is the session's identity. Token is its credential. They are
	// deliberately different values — see HashSessionToken.
	ID    string
	Token string

	UserID    string
	CreatedAt time.Time
	ExpiresAt time.Time

	// Authenticated is true when there is no second factor left to satisfy,
	// which is the ordinary case for an account without TOTP. False creates a
	// session that may do nothing but complete its second step.
	Authenticated bool
}

// TOTPEnrolment is a user's second factor, confirmed or in progress.
type TOTPEnrolment struct {
	UserID string

	// Secret is the shared secret, in bytes rather than base32.
	Secret []byte

	// ConfirmedAt is when the person first proved they could produce a code
	// from Secret. Zero means enrolment was started and never finished, and
	// such an enrolment is not a second factor.
	ConfirmedAt time.Time

	// LastUsedCounter is the highest counter a code has been accepted at. Pass
	// it to Authenticator.Validate as lastUsed.
	LastUsedCounter uint64

	CreatedAt time.Time
}

// Confirmed reports whether this enrolment is an active second factor rather
// than one that was started and abandoned.
func (e TOTPEnrolment) Confirmed() bool { return !e.ConfirmedAt.IsZero() }

// CredentialStore holds accounts and their passwords.
type CredentialStore interface {
	// Register creates an account. It returns ErrRegistrationClosed when the
	// instance already has one, which the database decides rather than the
	// caller: see the users_at_most_one_credentialed index.
	Register(ctx context.Context, r Registration) error

	// CredentialsByEmail looks an account up case-insensitively. It returns
	// ErrNoSuchUser when there is none — a distinction the HTTP layer must not
	// pass on, and which exists here so that layer can choose to hide it.
	CredentialsByEmail(ctx context.Context, email string) (Credentials, error)

	CredentialsByID(ctx context.Context, userID string) (Credentials, error)

	// SetPasswordHash replaces a password. It does not revoke sessions; that
	// is a separate, deliberate call, because "change my password" and "sign
	// out my other devices" are two decisions and the second is sometimes no.
	SetPasswordHash(ctx context.Context, userID, hash string, at time.Time) error

	// HasCredentialedUser reports whether anybody has registered yet.
	//
	// It answers what a registration form should offer, and nothing else. It
	// must never gate a registration: the answer is stale the instant it is
	// read, which is exactly the race the database index exists to settle.
	HasCredentialedUser(ctx context.Context) (bool, error)
}

// SessionStore holds sign-ins.
type SessionStore interface {
	CreateSession(ctx context.Context, s NewSession) error

	// SessionByToken resolves a cookie value to a session that may still be
	// acted on.
	//
	// It hashes the token itself, so no caller handles a stored credential. A
	// session that has expired, been revoked, or predates a password change is
	// reported as ErrSessionNotUsable rather than returned: the three ways of
	// being finished are one answer to a caller, and leaving the decision to
	// them means one of the three eventually gets forgotten.
	//
	// A session awaiting its second factor is returned, with AuthenticatedAt
	// zero. It has to be — it is the credential the second step presents — so
	// the caller reads Session.PendingSecondFactor and permits only that step.
	SessionByToken(ctx context.Context, token string, now time.Time) (Session, error)

	// ElevateSession marks the second factor satisfied and replaces the
	// token, which must be a freshly drawn one.
	//
	// The replacement is the point. A session that keeps its cookie across the
	// step that raises its privilege is a session fixation: whatever learned
	// the pre-authentication token holds a fully authenticated one the moment
	// the person finishes.
	ElevateSession(ctx context.Context, sessionID, newToken string, at time.Time) error

	RevokeSession(ctx context.Context, sessionID string, at time.Time) error

	// RevokeUserSessions withdraws everything belonging to one person, sparing
	// exceptID. Pass an empty string to spare nothing.
	RevokeUserSessions(ctx context.Context, userID, exceptID string, at time.Time) (int64, error)

	TouchSession(ctx context.Context, sessionID string, at time.Time) error

	UserSessions(ctx context.Context, userID string) ([]Session, error)
}

// SecondFactorStore holds TOTP enrolments and backup codes.
type SecondFactorStore interface {
	// BeginTOTPEnrolment stores a secret that has not been proved yet,
	// replacing any unfinished enrolment.
	BeginTOTPEnrolment(ctx context.Context, userID string, secret []byte, at time.Time) error

	// TOTPEnrolment reads the enrolment, confirmed or not. It returns
	// ErrNoTOTPEnrolment when there is none.
	TOTPEnrolment(ctx context.Context, userID string) (TOTPEnrolment, error)

	// ConfirmTOTPEnrolment finishes enrolment by checking a code against the
	// stored secret, inside the transaction that reads it.
	ConfirmTOTPEnrolment(ctx context.Context, a Authenticator, userID, code string, at time.Time) error

	// ConsumeTOTPCode verifies a code and spends the counter it matched, both
	// inside one transaction holding the enrolment row.
	//
	// It returns ErrCodeReused for a code whose counter has already been
	// spent, and ErrInvalidCode for one that matches no accepted window.
	ConsumeTOTPCode(ctx context.Context, a Authenticator, userID, code string, at time.Time) error

	DisableTOTP(ctx context.Context, userID string) error

	// ReplaceBackupCodes issues a new set, discarding the previous one
	// entirely — spent codes included, because somebody regenerating has
	// usually decided the old list is compromised.
	ReplaceBackupCodes(ctx context.Context, userID string, codes []string, at time.Time) error

	// ConsumeBackupCode spends a code, or returns ErrInvalidBackupCode. A code
	// already spent is indistinguishable from one that never existed, which is
	// the same answer either way.
	ConsumeBackupCode(ctx context.Context, userID, code string, at time.Time) error

	UnusedBackupCodeCount(ctx context.Context, userID string) (int, error)
}

// Event is one thing worth recording about authentication.
//
// It is deliberately narrower than a ledger audit entry: an authentication
// event has an actor, and only events with one are recorded here. Anonymous
// attempts — a sign-in against an address that does not exist — have no actor
// to attribute and are not written, for two reasons. The audit schema refuses
// a human origin without a human, which is what makes the rest of the log
// trustworthy; and writing a row on one login path and not the other would
// hand back through timing exactly the account enumeration the response
// wording and Hasher.VerifyDecoy exist to close.
//
// Failed attempts still have to be investigable. They go to the structured
// log, identically on both paths, where an operator can read them and an
// attacker cannot.
type Event struct {
	// ID is the entry's own identity, a canonical lowercase UUIDv7.
	ID string

	// ActorID is who did it. Required: an authentication event without a
	// person is not one this records.
	ActorID string

	// At is when it happened.
	At time.Time

	// Action names it: sign_in, sign_out, second_factor_verified,
	// backup_code_used, password_changed, totp_enrolled, totp_disabled.
	Action string

	// EntityKind and EntityID say what it was done to — usually the user
	// themselves, sometimes the session.
	EntityKind string
	EntityID   string

	// Detail is JSON describing the event. It must never carry a credential,
	// a token, or anything derived from one.
	Detail []byte
}

// EventRecorder writes authentication events.
type EventRecorder interface {
	RecordEvent(ctx context.Context, e Event) error
}
