// SPDX-License-Identifier: AGPL-3.0-only

package store_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/GaffaQ/Nusa/internal/auth"
	"github.com/GaffaQ/Nusa/internal/store"
)

// What these guards must cover. Written before they were, and each item was
// broken on purpose and watched failing afterwards — and checked against the
// test it was *expected* to fail, which is the rule the RFC 6238 counter claim
// produced (CLAUDE.md §11).
//
// Registration
//
//	 1. The first account registers; a second is refused by name.
//	 2. Two simultaneous registrations against an empty instance make one user.
//	 3. Credential-less actor rows neither count as accounts nor block one.
//	 4. A malformed identity and a NUL in an email are refused by name.
//
// Credentials
//
//	 5. Lookup by email is case-insensitive and preserves the address as typed.
//	 6. An unknown account, and an actor row, are both ErrNoSuchUser.
//	 7. A password change moves password_changed_at.
//
// Sessions
//
//	 8. A session round-trips by its token; a wrong token is ErrNoSession.
//	 9. Only the hash is stored — the token appears in no column.
//	10. Expired, revoked, and predating a password change are all unusable.
//	11. A session awaiting its second factor is returned, not refused.
//	12. Elevation rotates the token: the old one dies, the new one works.
//	13. Elevation is once only, and not on a finished session.
//	14. Revoking a user's sessions spares the named one and no other.
//	15. Sweeping removes what has expired.
//
// TOTP
//
//	16. An unconfirmed enrolment is not a second factor.
//	17. Confirming spends the counter that proved it.
//	18. A consumed code advances the counter; the same code again is a replay.
//	19. Two simultaneous submissions of one code: exactly one succeeds.
//	20. Re-enrolment resets confirmation and counter together.
//
// Backup codes
//
//	21. Codes are stored hashed; the code itself appears in no column.
//	22. A code is single use, and the count falls.
//	23. Two simultaneous submissions of one code: exactly one succeeds.
//	24. Regenerating discards the previous set, spent codes included.
//
// Idempotency response
//
//	25. Status and body round-trip, and half a response is refused.

const (
	testEmail    = "Budi.Santoso@Example.ORG"
	testPassword = "kopi tubruk gula aren"
)

func testHasher(t *testing.T) auth.Hasher {
	t.Helper()
	// Deliberately cheap. These tests are about rows, not about how long
	// Argon2id takes; the cost itself is measured in internal/auth.
	h, err := auth.NewHasher(auth.Params{
		Memory: 64, Iterations: 1, Parallelism: 1, SaltLength: 8, TagLength: 16,
	})
	require.NoError(t, err)
	return h
}

func registerUser(t *testing.T, s *store.Store, label, email string) (userID string, at time.Time) {
	t.Helper()

	hash, err := testHasher(t).Hash(testPassword)
	require.NoError(t, err)

	userID = testID(label)
	at = time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)
	require.NoError(t, s.Register(context.Background(), auth.Registration{
		UserID: userID, Email: email, PasswordHash: hash, At: at,
	}))
	return userID, at
}

// 1, 3.
func TestRegistrationIsOpenExactlyOnce(t *testing.T) {
	ctx := context.Background()
	s := open(t)

	has, err := s.HasCredentialedUser(ctx)
	require.NoError(t, err)
	require.False(t, has, "a fresh instance has nobody")

	// An actor row with no credentials must not look like a registration. These
	// exist so audit_log and idempotency_keys can point at something for a rule
	// or an import, and there is no reason the rule engine should close the
	// door on the household's first person.
	require.NoError(t, s.SaveUser(ctx, testID("rule-engine")))
	has, err = s.HasCredentialedUser(ctx)
	require.NoError(t, err)
	require.False(t, has, "an actor row is not an account")

	userID, _ := registerUser(t, s, "first", testEmail)
	require.NotEmpty(t, userID)

	has, err = s.HasCredentialedUser(ctx)
	require.NoError(t, err)
	require.True(t, has)

	hash, err := testHasher(t).Hash(testPassword)
	require.NoError(t, err)
	err = s.Register(ctx, auth.Registration{
		UserID: testID("second"), Email: "someone.else@example.org",
		PasswordHash: hash, At: time.Now().UTC(),
	})
	require.ErrorIs(t, err, auth.ErrRegistrationClosed)

	// And another actor row is still fine afterwards, because the rule is about
	// accounts rather than about rows in users.
	require.NoError(t, s.SaveUser(ctx, testID("importer")))
}

// 2. The race, arranged rather than hoped for.
//
// Both goroutines are held at the same point by a third transaction that has
// already inserted a credentialed user and not committed. Postgres blocks the
// second inserter on the uncommitted unique index entry, so when the gate
// resolves, both proceed against a decided state instead of racing.
//
// Two arrangements are exercised, because they fail differently. When the gate
// commits, both contenders lose to a row they can see. When it rolls back, the
// entry they were waiting on vanishes and exactly one of them wins — which is
// the case that actually proves the index and not merely the lock.
func TestTwoRegistrationsAtOnceMakeOneUser(t *testing.T) {
	for _, gateWins := range []bool{true, false} {
		name := "gate rolls back, one contender wins"
		if gateWins {
			name = "gate commits, both contenders lose"
		}

		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			s := open(t)

			hash, err := testHasher(t).Hash(testPassword)
			require.NoError(t, err)
			at := time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)

			gate, err := s.Pool().Begin(ctx)
			require.NoError(t, err)
			_, err = gate.Exec(ctx,
				`INSERT INTO users (id, email, password_hash, password_changed_at, created_at)
				 VALUES ($1, $2, $3, $4, $4)`,
				testID("gate"), "gate@example.org", hash, at)
			require.NoError(t, err)

			started := make(chan struct{}, 2)
			errs := make([]error, 2)
			var wg sync.WaitGroup
			wg.Add(2)
			for i, label := range []string{"race-a", "race-b"} {
				go func() {
					defer wg.Done()
					started <- struct{}{}
					errs[i] = s.Register(ctx, auth.Registration{
						UserID: testID(label), Email: label + "@example.org",
						PasswordHash: hash, At: at,
					})
				}()
			}

			<-started
			<-started
			// Both are now inside Register and heading for the same index
			// entry. The pause only lets them reach the database; the gate,
			// not the timing, is what guarantees neither has finished.
			time.Sleep(300 * time.Millisecond)
			if gateWins {
				require.NoError(t, gate.Commit(ctx))
			} else {
				require.NoError(t, gate.Rollback(ctx))
			}
			wg.Wait()

			succeeded := 0
			for _, err := range errs {
				if err == nil {
					succeeded++
					continue
				}
				require.ErrorIs(t, err, auth.ErrRegistrationClosed,
					"the loser must lose for the right reason, not on a raw constraint name")
			}

			want := 0
			if !gateWins {
				want = 1
			}
			require.Equal(t, want, succeeded)

			// The invariant that matters whatever the three writers did.
			var accounts int
			require.NoError(t, s.Pool().QueryRow(ctx,
				`SELECT count(*) FROM users WHERE email IS NOT NULL`).Scan(&accounts))
			require.Equal(t, 1, accounts, "an instance may hold exactly one account")
		})
	}
}

// 4.
func TestRegistrationRefusesMalformedInput(t *testing.T) {
	ctx := context.Background()
	s := open(t)

	hash, err := testHasher(t).Hash(testPassword)
	require.NoError(t, err)
	at := time.Now().UTC()

	t.Run("identity is not a uuidv7", func(t *testing.T) {
		err := s.Register(ctx, auth.Registration{
			UserID: "not-a-uuid", Email: "a@example.org", PasswordHash: hash, At: at,
		})
		require.ErrorIs(t, err, store.ErrInvalidWrite)
	})

	t.Run("nul in the email is named, not a raw sqlstate", func(t *testing.T) {
		err := s.Register(ctx, auth.Registration{
			UserID: testID("nul"), Email: "bu\x00di@example.org", PasswordHash: hash, At: at,
		})
		require.ErrorIs(t, err, store.ErrInvalidWrite)
		require.Contains(t, err.Error(), "email")
		require.NotContains(t, err.Error(), "22021",
			"the caller should be told which field, not handed a SQLSTATE")
	})

	t.Run("missing email or hash", func(t *testing.T) {
		require.ErrorIs(t, s.Register(ctx, auth.Registration{
			UserID: testID("no-email"), PasswordHash: hash, At: at,
		}), store.ErrInvalidWrite)
		require.ErrorIs(t, s.Register(ctx, auth.Registration{
			UserID: testID("no-hash"), Email: "a@example.org", At: at,
		}), store.ErrInvalidWrite)
	})
}

// 5, 6.
func TestCredentialsAreFoundCaseInsensitivelyAndStoredAsTyped(t *testing.T) {
	ctx := context.Background()
	s := open(t)

	userID, at := registerUser(t, s, "person", testEmail)
	require.NoError(t, s.SaveUser(ctx, testID("actor")))

	for _, written := range []string{
		testEmail,
		"budi.santoso@example.org",
		"BUDI.SANTOSO@EXAMPLE.ORG",
		"BuDi.SaNtOsO@eXaMpLe.OrG",
	} {
		got, err := s.CredentialsByEmail(ctx, written)
		require.NoError(t, err, "looking up %q", written)
		require.Equal(t, userID, got.UserID)
		require.Equal(t, testEmail, got.Email,
			"the address must come back as it was typed, not folded")
		require.Equal(t, at.UTC(), got.PasswordChangedAt.UTC())
		require.True(t, auth.Verify(got.PasswordHash, testPassword) == nil,
			"the stored hash must verify the password it was made from")
	}

	byID, err := s.CredentialsByID(ctx, userID)
	require.NoError(t, err)
	require.Equal(t, testEmail, byID.Email)

	_, err = s.CredentialsByEmail(ctx, "nobody@example.org")
	require.ErrorIs(t, err, auth.ErrNoSuchUser)

	// An actor row exists but holds no credentials. Reporting it as a user with
	// an empty hash would invite something to compare a password against "".
	_, err = s.CredentialsByID(ctx, testID("actor"))
	require.ErrorIs(t, err, auth.ErrNoSuchUser)

	_, err = s.CredentialsByID(ctx, testID("never-created"))
	require.ErrorIs(t, err, auth.ErrNoSuchUser)
}

// 7, and the second half of 10.
func TestChangingAPasswordEndsSessionsThatPredateIt(t *testing.T) {
	ctx := context.Background()
	s := open(t)

	userID, at := registerUser(t, s, "person", testEmail)

	token, err := auth.NewSessionToken()
	require.NoError(t, err)
	require.NoError(t, s.CreateSession(ctx, auth.NewSession{
		ID: testID("session"), UserID: userID, Token: token,
		CreatedAt: at, ExpiresAt: at.Add(24 * time.Hour), Authenticated: true,
	}))

	_, err = s.SessionByToken(ctx, token, at.Add(time.Hour))
	require.NoError(t, err, "the session works before the password changes")

	newHash, err := testHasher(t).Hash("something else entirely")
	require.NoError(t, err)
	changed := at.Add(2 * time.Hour)
	require.NoError(t, s.SetPasswordHash(ctx, userID, newHash, changed))

	after, err := s.CredentialsByID(ctx, userID)
	require.NoError(t, err)
	require.Equal(t, changed.UTC(), after.PasswordChangedAt.UTC())
	require.NoError(t, auth.Verify(after.PasswordHash, "something else entirely"))
	require.ErrorIs(t, auth.Verify(after.PasswordHash, testPassword), auth.ErrPasswordMismatch)

	// The session was never revoked and its row is untouched, yet it is
	// finished. This is the backstop: a caller that changes a password and
	// forgets to revoke has still ended the old sign-ins.
	_, err = s.SessionByToken(ctx, token, changed.Add(time.Minute))
	require.ErrorIs(t, err, auth.ErrSessionNotUsable)

	require.ErrorIs(t, s.SetPasswordHash(ctx, testID("nobody"), newHash, changed), auth.ErrNoSuchUser)
}

// 8, 9, 10, 11.
func TestSessionLookupDecidesUsability(t *testing.T) {
	ctx := context.Background()
	s := open(t)

	userID, at := registerUser(t, s, "person", testEmail)
	newSession := func(label string, expires time.Duration, authenticated bool) string {
		t.Helper()
		token, err := auth.NewSessionToken()
		require.NoError(t, err)
		require.NoError(t, s.CreateSession(ctx, auth.NewSession{
			ID: testID(label), UserID: userID, Token: token,
			CreatedAt: at, ExpiresAt: at.Add(expires), Authenticated: authenticated,
		}))
		return token
	}

	t.Run("round trip, and the token is nowhere in the row", func(t *testing.T) {
		token := newSession("live", time.Hour, true)

		got, err := s.SessionByToken(ctx, token, at.Add(time.Minute))
		require.NoError(t, err)
		require.Equal(t, testID("live"), got.ID)
		require.Equal(t, userID, got.UserID)
		require.False(t, got.PendingSecondFactor())

		// The credential must not be recoverable from the database. Casting the
		// stored hash to text and searching for the token is the same thing an
		// attacker with a dump would do.
		var matches int
		require.NoError(t, s.Pool().QueryRow(ctx,
			`SELECT count(*) FROM sessions WHERE encode(token_hash, 'escape') LIKE '%' || $1 || '%'`,
			token).Scan(&matches))
		require.Zero(t, matches, "the token itself must not be stored")

		var stored []byte
		require.NoError(t, s.Pool().QueryRow(ctx,
			`SELECT token_hash FROM sessions WHERE id = $1`, testID("live")).Scan(&stored))
		require.Equal(t, auth.HashSessionToken(token), stored)
		require.Len(t, stored, 32)
	})

	t.Run("an unknown token", func(t *testing.T) {
		other, err := auth.NewSessionToken()
		require.NoError(t, err)
		_, err = s.SessionByToken(ctx, other, at)
		require.ErrorIs(t, err, auth.ErrNoSession)

		_, err = s.SessionByToken(ctx, "", at)
		require.ErrorIs(t, err, auth.ErrNoSession)
	})

	t.Run("expired", func(t *testing.T) {
		token := newSession("expiring", time.Hour, true)
		_, err := s.SessionByToken(ctx, token, at.Add(59*time.Minute))
		require.NoError(t, err)

		// The boundary: expires_at is not itself inside the session.
		_, err = s.SessionByToken(ctx, token, at.Add(time.Hour))
		require.ErrorIs(t, err, auth.ErrSessionNotUsable)
	})

	t.Run("awaiting a second factor is a state, not a refusal", func(t *testing.T) {
		token := newSession("pending", time.Hour, false)

		got, err := s.SessionByToken(ctx, token, at.Add(time.Minute))
		require.NoError(t, err,
			"refusing this would make completing two-factor authentication impossible")
		require.True(t, got.PendingSecondFactor())
		require.True(t, got.AuthenticatedAt.IsZero())
	})
}

// 10, the part that matters most: a running session is genuinely closed.
//
// What this proves is that the lookup every request performs stops answering.
// It does not prove that a request already in flight, holding a Session value
// it read a moment ago, is interrupted — nothing at this layer can, because
// that is a property of the middleware and its handler. That half is step 7's
// and is recorded as such rather than quietly counted here.
func TestRevokingASessionClosesIt(t *testing.T) {
	ctx := context.Background()
	s := open(t)

	userID, at := registerUser(t, s, "person", testEmail)
	token, err := auth.NewSessionToken()
	require.NoError(t, err)
	require.NoError(t, s.CreateSession(ctx, auth.NewSession{
		ID: testID("victim"), UserID: userID, Token: token,
		CreatedAt: at, ExpiresAt: at.Add(24 * time.Hour), Authenticated: true,
	}))

	// It works, repeatedly, before revocation. Checking twice matters: a
	// lookup that consumed the session would pass a single check.
	for range 3 {
		_, err := s.SessionByToken(ctx, token, at.Add(time.Minute))
		require.NoError(t, err)
	}

	revoked := at.Add(time.Hour)
	require.NoError(t, s.RevokeSession(ctx, testID("victim"), revoked))

	// Presenting the very same cookie now fails, at the same instant it
	// previously succeeded.
	_, err = s.SessionByToken(ctx, token, at.Add(time.Minute))
	require.ErrorIs(t, err, auth.ErrSessionNotUsable)
	_, err = s.SessionByToken(ctx, token, revoked.Add(time.Second))
	require.ErrorIs(t, err, auth.ErrSessionNotUsable)

	// Withdrawn, not deleted: the row survives so the revocation can be shown.
	sessions, err := s.UserSessions(ctx, userID)
	require.NoError(t, err)
	require.Len(t, sessions, 1)
	require.Equal(t, revoked.UTC(), sessions[0].RevokedAt.UTC())

	// Revoking again keeps the first time rather than overwriting it.
	require.NoError(t, s.RevokeSession(ctx, testID("victim"), revoked.Add(time.Hour)))
	sessions, err = s.UserSessions(ctx, userID)
	require.NoError(t, err)
	require.Equal(t, revoked.UTC(), sessions[0].RevokedAt.UTC(),
		"the first revocation is when it ended")

	require.ErrorIs(t, s.RevokeSession(ctx, testID("no-such-session"), revoked), auth.ErrNoSession)
}

// 12, 13.
func TestElevationRotatesTheToken(t *testing.T) {
	ctx := context.Background()
	s := open(t)

	userID, at := registerUser(t, s, "person", testEmail)
	before, err := auth.NewSessionToken()
	require.NoError(t, err)
	require.NoError(t, s.CreateSession(ctx, auth.NewSession{
		ID: testID("elevating"), UserID: userID, Token: before,
		CreatedAt: at, ExpiresAt: at.Add(time.Hour), Authenticated: false,
	}))

	// The real flow finds the session by the cookie it was handed at the
	// password step, then elevates it. Looking it up here rather than only
	// using the id is what makes this test notice if pending sessions ever
	// stop being resolvable — without it, refusing them at lookup would break
	// two-factor authentication in production and pass here.
	pending, err := s.SessionByToken(ctx, before, at.Add(time.Second))
	require.NoError(t, err)
	require.True(t, pending.PendingSecondFactor())
	require.Equal(t, testID("elevating"), pending.ID)

	after, err := auth.NewSessionToken()
	require.NoError(t, err)
	require.NotEqual(t, before, after)

	elevated := at.Add(time.Minute)
	require.NoError(t, s.ElevateSession(ctx, testID("elevating"), after, elevated))

	// The pre-authentication token is dead. This is the whole point: anything
	// that learned it before the second factor was satisfied does not now hold
	// a fully authenticated session.
	_, err = s.SessionByToken(ctx, before, elevated)
	require.ErrorIs(t, err, auth.ErrNoSession,
		"the old token must no longer resolve to anything")

	got, err := s.SessionByToken(ctx, after, elevated)
	require.NoError(t, err)
	require.Equal(t, testID("elevating"), got.ID, "it is the same session, not a new one")
	require.False(t, got.PendingSecondFactor())
	require.Equal(t, elevated.UTC(), got.AuthenticatedAt.UTC())

	t.Run("only once", func(t *testing.T) {
		third, err := auth.NewSessionToken()
		require.NoError(t, err)
		require.ErrorIs(t,
			s.ElevateSession(ctx, testID("elevating"), third, elevated.Add(time.Minute)),
			auth.ErrSessionNotUsable)

		// And the second attempt did not quietly install the third token.
		_, err = s.SessionByToken(ctx, third, elevated.Add(time.Minute))
		require.ErrorIs(t, err, auth.ErrNoSession)
		_, err = s.SessionByToken(ctx, after, elevated.Add(time.Minute))
		require.NoError(t, err, "the token from the successful elevation still works")
	})

	t.Run("not a revoked session", func(t *testing.T) {
		token, err := auth.NewSessionToken()
		require.NoError(t, err)
		require.NoError(t, s.CreateSession(ctx, auth.NewSession{
			ID: testID("revoked-then-elevated"), UserID: userID, Token: token,
			CreatedAt: at, ExpiresAt: at.Add(time.Hour), Authenticated: false,
		}))
		require.NoError(t, s.RevokeSession(ctx, testID("revoked-then-elevated"), at.Add(time.Minute)))

		replacement, err := auth.NewSessionToken()
		require.NoError(t, err)
		require.ErrorIs(t,
			s.ElevateSession(ctx, testID("revoked-then-elevated"), replacement, at.Add(2*time.Minute)),
			auth.ErrSessionNotUsable)
	})

	t.Run("not an expired session", func(t *testing.T) {
		token, err := auth.NewSessionToken()
		require.NoError(t, err)
		require.NoError(t, s.CreateSession(ctx, auth.NewSession{
			ID: testID("expired-then-elevated"), UserID: userID, Token: token,
			CreatedAt: at, ExpiresAt: at.Add(time.Minute), Authenticated: false,
		}))

		replacement, err := auth.NewSessionToken()
		require.NoError(t, err)
		require.ErrorIs(t,
			s.ElevateSession(ctx, testID("expired-then-elevated"), replacement, at.Add(time.Hour)),
			auth.ErrSessionNotUsable)
	})
}

// 14, 15.
func TestRevokingEverythingSparesExactlyTheNamedSession(t *testing.T) {
	ctx := context.Background()
	s := open(t)

	userID, at := registerUser(t, s, "person", testEmail)
	tokens := map[string]string{}
	for _, label := range []string{"phone", "laptop", "tablet"} {
		token, err := auth.NewSessionToken()
		require.NoError(t, err)
		require.NoError(t, s.CreateSession(ctx, auth.NewSession{
			ID: testID(label), UserID: userID, Token: token,
			CreatedAt: at, ExpiresAt: at.Add(24 * time.Hour), Authenticated: true,
		}))
		tokens[label] = token
	}

	revoked, err := s.RevokeUserSessions(ctx, userID, testID("laptop"), at.Add(time.Hour))
	require.NoError(t, err)
	require.Equal(t, int64(2), revoked)

	_, err = s.SessionByToken(ctx, tokens["laptop"], at.Add(2*time.Hour))
	require.NoError(t, err, "the spared session keeps working")
	for _, label := range []string{"phone", "tablet"} {
		_, err := s.SessionByToken(ctx, tokens[label], at.Add(2*time.Hour))
		require.ErrorIs(t, err, auth.ErrSessionNotUsable, "%s should be gone", label)
	}

	t.Run("sparing nothing", func(t *testing.T) {
		// An empty exception must mean "spare nothing" rather than "spare
		// whatever a NULL comparison happens to do", and `id <> NULL` is never
		// true — which would spare everything instead.
		n, err := s.RevokeUserSessions(ctx, userID, "", at.Add(3*time.Hour))
		require.NoError(t, err)
		require.Equal(t, int64(1), n, "only the previously spared session was left to revoke")

		_, err = s.SessionByToken(ctx, tokens["laptop"], at.Add(4*time.Hour))
		require.ErrorIs(t, err, auth.ErrSessionNotUsable)
	})

	t.Run("sweeping what has expired", func(t *testing.T) {
		before, err := s.UserSessions(ctx, userID)
		require.NoError(t, err)
		require.Len(t, before, 3)

		n, err := s.SweepExpiredSessions(ctx, at.Add(48*time.Hour))
		require.NoError(t, err)
		require.Equal(t, int64(3), n)

		after, err := s.UserSessions(ctx, userID)
		require.NoError(t, err)
		require.Empty(t, after)
	})
}
