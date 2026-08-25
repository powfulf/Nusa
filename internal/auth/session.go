// SPDX-License-Identifier: AGPL-3.0-only

package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"time"
)

// SessionTokenBytes is 32: 256 bits of entropy in the cookie.
//
// The size is chosen against online guessing rather than offline cracking,
// because a session token is only ever presented to a live server. Even so
// there is no reason to economise — the token is copied by a browser, not typed
// by a person, so length costs nothing and 256 bits removes the question
// entirely.
const SessionTokenBytes = 32

// NewSessionToken draws the value that goes in the cookie.
//
// It is base64url without padding so it survives a Set-Cookie header untouched:
// the standard alphabet's '+' and '/' are legal in a cookie value but are
// exactly the characters that get re-encoded by something in the middle, and a
// token that arrives back altered is a session that silently stops working.
func NewSessionToken() (string, error) {
	raw := make([]byte, SessionTokenBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("draw session token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// HashSessionToken returns what is stored for a token.
//
// SHA-256 rather than Argon2id, for the reason set out at the top of
// backupcode.go: the token is drawn rather than chosen, so there is no
// distribution for a slow hash to defend against — and this runs on every
// authenticated request, where a memory-hard hash would turn each page load
// into 19 MiB of work.
//
// Storing the hash rather than the token means a leaked database yields no
// usable cookie. It is also why a session's identity and its credential are two
// different values: the id appears in logs and in the audit trail, while the
// token exists only in the response that set it.
func HashSessionToken(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}

// Session is a live sign-in, as the store hands it back.
//
// It carries no token: by the time a Session exists the token has already been
// matched, and passing it further would mean copying a credential into places
// that only need to know who is signed in.
type Session struct {
	// ID identifies the session in logs and in the audit trail.
	ID string

	// UserID is who is signed in.
	UserID string

	// CreatedAt is when the password was accepted.
	CreatedAt time.Time

	// LastSeenAt is when the session was last used, as often as the caller
	// chooses to record it.
	LastSeenAt time.Time

	// ExpiresAt is when the session stops being usable regardless of activity.
	ExpiresAt time.Time

	// AuthenticatedAt is when the second factor was satisfied, and is zero
	// while it has not been. A session in that state may do exactly two
	// things: complete its second step, or be abandoned.
	AuthenticatedAt time.Time

	// RevokedAt is when the session was withdrawn, and is zero while it is
	// live. A revoked session is kept rather than deleted so that "this was
	// revoked" stays answerable.
	RevokedAt time.Time
}

// PendingSecondFactor reports a session whose password step is done and whose
// second factor is not.
func (s Session) PendingSecondFactor() bool { return s.AuthenticatedAt.IsZero() }
