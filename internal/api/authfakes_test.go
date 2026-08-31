// SPDX-License-Identifier: AGPL-3.0-only

package api_test

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/GaffaQ/Nusa/internal/auth"
)

// In-memory stand-ins for the three repositories the handlers use.
//
// They are fakes rather than mocks: each one implements the behaviour the real
// store was proved to have, so a handler test exercises the same rules without
// a container. Where a fake would be free to be lax — a revoked session still
// resolving, an elevation working twice — it is not, because those are exactly
// the properties the handlers are being tested against.
//
// The real implementations are covered by the integration suite in
// internal/store. Nothing here is a substitute for that; it is a substitute for
// Docker in tests that are about HTTP.

type fakeCredentials struct {
	mu    sync.Mutex
	users map[string]*auth.Credentials // keyed by lowercased email
	byID  map[string]*auth.Credentials
	// closed mirrors users_at_most_one_credentialed.
	failWith error
}

func newFakeCredentials() *fakeCredentials {
	return &fakeCredentials{
		users: map[string]*auth.Credentials{},
		byID:  map[string]*auth.Credentials{},
	}
}

func (f *fakeCredentials) Register(_ context.Context, r auth.Registration) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failWith != nil {
		return f.failWith
	}
	if len(f.users) > 0 {
		if _, taken := f.users[strings.ToLower(r.Email)]; taken {
			return auth.ErrEmailTaken
		}
		return auth.ErrRegistrationClosed
	}
	c := &auth.Credentials{
		UserID: r.UserID, Email: r.Email, PasswordHash: r.PasswordHash,
		PasswordChangedAt: r.At, CreatedAt: r.At,
	}
	f.users[strings.ToLower(r.Email)] = c
	f.byID[r.UserID] = c
	return nil
}

func (f *fakeCredentials) CredentialsByEmail(_ context.Context, email string) (auth.Credentials, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c, ok := f.users[strings.ToLower(strings.TrimSpace(email))]
	if !ok {
		return auth.Credentials{}, auth.ErrNoSuchUser
	}
	return *c, nil
}

func (f *fakeCredentials) CredentialsByID(_ context.Context, id string) (auth.Credentials, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c, ok := f.byID[id]
	if !ok {
		return auth.Credentials{}, auth.ErrNoSuchUser
	}
	return *c, nil
}

func (f *fakeCredentials) SetPasswordHash(_ context.Context, id, hash string, at time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	c, ok := f.byID[id]
	if !ok {
		return auth.ErrNoSuchUser
	}
	c.PasswordHash, c.PasswordChangedAt = hash, at
	return nil
}

func (f *fakeCredentials) HasCredentialedUser(context.Context) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.users) > 0, nil
}

type storedSession struct {
	auth.Session
	tokenHash string
}

type fakeSessions struct {
	mu       sync.Mutex
	byID     map[string]*storedSession
	creds    *fakeCredentials
	lookups  int
	failWith error
}

func newFakeSessions(creds *fakeCredentials) *fakeSessions {
	return &fakeSessions{byID: map[string]*storedSession{}, creds: creds}
}

func (f *fakeSessions) CreateSession(_ context.Context, ns auth.NewSession) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	s := &storedSession{
		Session: auth.Session{
			ID: ns.ID, UserID: ns.UserID, CreatedAt: ns.CreatedAt,
			LastSeenAt: ns.CreatedAt, ExpiresAt: ns.ExpiresAt,
		},
		tokenHash: string(auth.HashSessionToken(ns.Token)),
	}
	if ns.Authenticated {
		s.AuthenticatedAt = ns.CreatedAt
	}
	f.byID[ns.ID] = s
	return nil
}

// Lookups reports how many times SessionByToken was called, which is how the
// "nothing is cached" property is measured.
func (f *fakeSessions) Lookups() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lookups
}

func (f *fakeSessions) SessionByToken(_ context.Context, token string, now time.Time) (auth.Session, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lookups++
	if f.failWith != nil {
		return auth.Session{}, f.failWith
	}

	hash := string(auth.HashSessionToken(token))
	for _, s := range f.byID {
		if s.tokenHash != hash {
			continue
		}
		switch {
		case !s.RevokedAt.IsZero():
			return auth.Session{}, auth.ErrSessionNotUsable
		case !s.ExpiresAt.After(now):
			return auth.Session{}, auth.ErrSessionNotUsable
		}
		if f.creds != nil {
			if c, ok := f.creds.byID[s.UserID]; ok &&
				!c.PasswordChangedAt.IsZero() && c.PasswordChangedAt.After(s.CreatedAt) {
				return auth.Session{}, auth.ErrSessionNotUsable
			}
		}
		return s.Session, nil
	}
	return auth.Session{}, auth.ErrNoSession
}

func (f *fakeSessions) ElevateSession(_ context.Context, id, newToken string, at time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.byID[id]
	if !ok || !s.RevokedAt.IsZero() || !s.ExpiresAt.After(at) || !s.AuthenticatedAt.IsZero() {
		return auth.ErrSessionNotUsable
	}
	s.tokenHash = string(auth.HashSessionToken(newToken))
	s.AuthenticatedAt = at
	return nil
}

func (f *fakeSessions) RevokeSession(_ context.Context, id string, at time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.byID[id]
	if !ok {
		return auth.ErrNoSession
	}
	if s.RevokedAt.IsZero() {
		s.RevokedAt = at
	}
	return nil
}

func (f *fakeSessions) RevokeUserSessions(_ context.Context, userID, except string, at time.Time) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var n int64
	for id, s := range f.byID {
		if s.UserID == userID && id != except && s.RevokedAt.IsZero() {
			s.RevokedAt = at
			n++
		}
	}
	return n, nil
}

func (f *fakeSessions) TouchSession(_ context.Context, id string, at time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if s, ok := f.byID[id]; ok {
		s.LastSeenAt = at
	}
	return nil
}

func (f *fakeSessions) UserSessions(_ context.Context, userID string) ([]auth.Session, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []auth.Session
	for _, s := range f.byID {
		if s.UserID == userID {
			out = append(out, s.Session)
		}
	}
	return out, nil
}

type fakeSecondFactors struct {
	mu         sync.Mutex
	enrolments map[string]*auth.TOTPEnrolment
	backup     map[string][]string // user -> unspent plaintext codes
	spentTOTP  map[string]uint64
}

func newFakeSecondFactors() *fakeSecondFactors {
	return &fakeSecondFactors{
		enrolments: map[string]*auth.TOTPEnrolment{},
		backup:     map[string][]string{},
		spentTOTP:  map[string]uint64{},
	}
}

func (f *fakeSecondFactors) BeginTOTPEnrolment(_ context.Context, userID string, secret []byte, at time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.enrolments[userID] = &auth.TOTPEnrolment{UserID: userID, Secret: secret, CreatedAt: at}
	return nil
}

// confirm marks an enrolment usable, which is what the real store does after a
// code proves it.
func (f *fakeSecondFactors) confirm(userID string, at time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.enrolments[userID].ConfirmedAt = at
}

func (f *fakeSecondFactors) TOTPEnrolment(_ context.Context, userID string) (auth.TOTPEnrolment, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	e, ok := f.enrolments[userID]
	if !ok {
		return auth.TOTPEnrolment{}, auth.ErrNoTOTPEnrolment
	}
	return *e, nil
}

func (f *fakeSecondFactors) ConfirmTOTPEnrolment(
	_ context.Context, a auth.Authenticator, userID, code string, at time.Time,
) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	e, ok := f.enrolments[userID]
	if !ok {
		return auth.ErrNoTOTPEnrolment
	}
	if !e.ConfirmedAt.IsZero() {
		return auth.ErrTOTPAlreadyConfirmed
	}
	counter, err := a.Validate(e.Secret, code, at, 0)
	if err != nil {
		return err
	}
	// Confirming spends the counter that proved it, as the real store does, so
	// the code somebody typed to finish enrolling is not then usable to sign in.
	f.spentTOTP[userID] = counter
	e.ConfirmedAt = at
	return nil
}

func (f *fakeSecondFactors) ConsumeTOTPCode(
	_ context.Context, a auth.Authenticator, userID, code string, at time.Time,
) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	e, ok := f.enrolments[userID]
	if !ok {
		return auth.ErrNoTOTPEnrolment
	}
	if e.ConfirmedAt.IsZero() {
		// An enrolment that was started and never proved is not a second
		// factor, and the real store says so too.
		return auth.ErrTOTPNotConfirmed
	}
	counter, err := a.Validate(e.Secret, code, at, f.spentTOTP[userID])
	if err != nil {
		return err
	}
	f.spentTOTP[userID] = counter
	return nil
}

func (f *fakeSecondFactors) DisableTOTP(_ context.Context, userID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.enrolments, userID)
	return nil
}

// ReplaceBackupCodes takes HASHES, exactly as the real store does. The
// distinction is the whole contract: a stored plaintext backup code is a
// password kept in clear, and a fake that accepted one would let a handler
// store plaintext and still pass.
//
// This fake used to keep whatever it was handed and compare it as plaintext,
// which made an existing test hollow — it seeded plaintext, submitted
// plaintext, and proved nothing about a path where the two differ (§11).
func (f *fakeSecondFactors) ReplaceBackupCodes(_ context.Context, userID string, hashes []string, _ time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.backup[userID] = append([]string(nil), hashes...)
	return nil
}

// ConsumeBackupCode takes a PLAINTEXT code and matches it against the stored
// hashes, through the same function the real store uses. Normalisation —
// case, spaces, the grouping hyphens — lives in there, so this fake cannot
// disagree with the real one about what counts as the same code.
func (f *fakeSecondFactors) ConsumeBackupCode(_ context.Context, userID, code string, _ time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	index, err := auth.MatchBackupCode(f.backup[userID], code)
	if err != nil {
		return err
	}
	f.backup[userID] = append(f.backup[userID][:index], f.backup[userID][index+1:]...)
	return nil
}

func (f *fakeSecondFactors) UnusedBackupCodeCount(_ context.Context, userID string) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.backup[userID]), nil
}

type fakeEvents struct {
	mu       sync.Mutex
	recorded []auth.Event
}

func (f *fakeEvents) RecordEvent(_ context.Context, e auth.Event) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.recorded = append(f.recorded, e)
	return nil
}

func (f *fakeEvents) actions() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, 0, len(f.recorded))
	for _, e := range f.recorded {
		out = append(out, e.Action)
	}
	return out
}

// countingHasher wraps a real Hasher and counts decoy verifications.
//
// This is the whole reason PasswordHasher is an interface. VerifyDecoy has no
// return value and no observable effect, so proving the no-such-account path
// calls it needs either a counter or a stopwatch — and a stopwatch in CI is a
// flake waiting to happen.
type countingHasher struct {
	inner  auth.Hasher
	mu     sync.Mutex
	decoys int
}

func (h *countingHasher) Hash(password string) (string, error) { return h.inner.Hash(password) }
func (h *countingHasher) NeedsRehash(encoded string) (bool, error) {
	return h.inner.NeedsRehash(encoded)
}
func (h *countingHasher) VerifyDecoy() {
	h.mu.Lock()
	h.decoys++
	h.mu.Unlock()
	h.inner.VerifyDecoy()
}

func (h *countingHasher) decoyCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.decoys
}
