// SPDX-License-Identifier: AGPL-3.0-only

package store_test

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/powfulf/Nusa/internal/auth"
	"github.com/powfulf/Nusa/internal/store"
)

// Items 16 to 24 of the coverage list in auth_test.go.

// enrolled registers a user and puts a confirmed TOTP enrolment on them,
// returning the secret and the instant the enrolment was confirmed.
func enrolled(t *testing.T, s *store.Store) (userID string, secret []byte, at time.Time) {
	t.Helper()
	ctx := context.Background()

	userID, at = registerUser(t, s, "person", testEmail)

	secret, err := auth.NewSecret()
	require.NoError(t, err)
	require.NoError(t, s.BeginTOTPEnrolment(ctx, userID, secret, at))

	a := auth.DefaultAuthenticator()
	code, err := a.Code(secret, at)
	require.NoError(t, err)
	require.NoError(t, s.ConfirmTOTPEnrolment(ctx, a, userID, code, at))

	return userID, secret, at
}

// 16, 17.
func TestAnUnconfirmedEnrolmentIsNotASecondFactor(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	a := auth.DefaultAuthenticator()

	userID, at := registerUser(t, s, "person", testEmail)

	_, err := s.TOTPEnrolment(ctx, userID)
	require.ErrorIs(t, err, auth.ErrNoTOTPEnrolment)
	require.ErrorIs(t, s.ConsumeTOTPCode(ctx, a, userID, "123456", at), auth.ErrNoTOTPEnrolment)

	secret, err := auth.NewSecret()
	require.NoError(t, err)
	require.NoError(t, s.BeginTOTPEnrolment(ctx, userID, secret, at))

	pending, err := s.TOTPEnrolment(ctx, userID)
	require.NoError(t, err)
	require.False(t, pending.Confirmed())
	require.Equal(t, secret, pending.Secret)
	require.Zero(t, pending.LastUsedCounter)

	// A secret that has been generated but never proved is not a second
	// factor. Accepting codes against it would lock somebody out with a QR
	// code they closed without scanning.
	code, err := a.Code(secret, at)
	require.NoError(t, err)
	require.ErrorIs(t, s.ConsumeTOTPCode(ctx, a, userID, code, at), auth.ErrTOTPNotConfirmed)

	t.Run("a wrong code does not confirm", func(t *testing.T) {
		require.ErrorIs(t, s.ConfirmTOTPEnrolment(ctx, a, userID, "000000", at), auth.ErrInvalidCode)
		still, err := s.TOTPEnrolment(ctx, userID)
		require.NoError(t, err)
		require.False(t, still.Confirmed())
	})

	t.Run("confirming spends the code that proved it", func(t *testing.T) {
		require.NoError(t, s.ConfirmTOTPEnrolment(ctx, a, userID, code, at))

		confirmed, err := s.TOTPEnrolment(ctx, userID)
		require.NoError(t, err)
		require.True(t, confirmed.Confirmed())
		require.NotZero(t, confirmed.LastUsedCounter,
			"the counter that proved the enrolment must be spent")

		// The very code someone typed to finish enrolling must not then be
		// usable as a login. Without this, the enrolment step would hand an
		// eavesdropper a working second factor for the next 60 to 90 seconds.
		require.ErrorIs(t, s.ConsumeTOTPCode(ctx, a, userID, code, at), auth.ErrCodeReused)
	})

	t.Run("confirming twice", func(t *testing.T) {
		later, err := a.Code(secret, at.Add(time.Minute))
		require.NoError(t, err)
		require.ErrorIs(t,
			s.ConfirmTOTPEnrolment(ctx, a, userID, later, at.Add(time.Minute)),
			auth.ErrTOTPAlreadyConfirmed)
	})
}

// 18.
func TestConsumingACodeSpendsItsCounter(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	a := auth.DefaultAuthenticator()

	userID, secret, at := enrolled(t, s)

	// A fresh window, past the one enrolment spent.
	now := at.Add(2 * time.Minute)
	code, err := a.Code(secret, now)
	require.NoError(t, err)

	require.NoError(t, s.ConsumeTOTPCode(ctx, a, userID, code, now))

	after, err := s.TOTPEnrolment(ctx, userID)
	require.NoError(t, err)
	require.Equal(t, uint64(now.Unix()/30), after.LastUsedCounter)

	t.Run("the same code again is a replay, not a wrong code", func(t *testing.T) {
		err := s.ConsumeTOTPCode(ctx, a, userID, code, now)
		require.ErrorIs(t, err, auth.ErrCodeReused)
		require.NotErrorIs(t, err, auth.ErrInvalidCode)
	})

	t.Run("still a replay one window later, while skew still admits it", func(t *testing.T) {
		// This is the case the counter exists for. At 30-second periods with a
		// skew of one, the code is genuinely still acceptable here — so
		// without a stored counter this would succeed.
		require.ErrorIs(t,
			s.ConsumeTOTPCode(ctx, a, userID, code, now.Add(30*time.Second)),
			auth.ErrCodeReused)
	})

	t.Run("the next window's code is not", func(t *testing.T) {
		next := now.Add(30 * time.Second)
		code, err := a.Code(secret, next)
		require.NoError(t, err)
		require.NoError(t, s.ConsumeTOTPCode(ctx, a, userID, code, next))
	})

	t.Run("a wrong code", func(t *testing.T) {
		err := s.ConsumeTOTPCode(ctx, a, userID, "000000", now.Add(5*time.Minute))
		require.ErrorIs(t, err, auth.ErrInvalidCode)
	})
}

// 19. The race, arranged rather than hoped for.
//
// A third transaction holds the enrolment row, so both submissions are stopped
// at the same point before either can read the counter. When the gate releases
// them they proceed one after the other, and the second re-reads a counter the
// first has already advanced.
//
// Without FOR UPDATE, MVCC lets both read the original counter — a plain read
// is never blocked by a row lock — and both then find the code unspent and
// both succeed. That is the replay the counter exists to prevent, arriving
// through the door left open by deciding outside the transaction.
func TestTwoSubmissionsOfOneCodeCannotBothSucceed(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	a := auth.DefaultAuthenticator()

	userID, secret, at := enrolled(t, s)
	now := at.Add(2 * time.Minute)
	code, err := a.Code(secret, now)
	require.NoError(t, err)

	gate, err := s.Pool().Begin(ctx)
	require.NoError(t, err)
	_, err = gate.Exec(ctx, `SELECT user_id FROM user_totp WHERE user_id = $1 FOR UPDATE`, userID)
	require.NoError(t, err)

	started := make(chan struct{}, 2)
	errs := make([]error, 2)
	var wg sync.WaitGroup
	wg.Add(2)
	for i := range 2 {
		go func() {
			defer wg.Done()
			started <- struct{}{}
			errs[i] = s.ConsumeTOTPCode(ctx, a, userID, code, now)
		}()
	}

	<-started
	<-started
	// Both are inside ConsumeTOTPCode and heading for the same row. The pause
	// only lets them reach the database; the gate, not the timing, is what
	// guarantees neither has finished.
	time.Sleep(300 * time.Millisecond)
	require.NoError(t, gate.Rollback(ctx))
	wg.Wait()

	succeeded := 0
	for _, err := range errs {
		if err == nil {
			succeeded++
			continue
		}
		require.ErrorIs(t, err, auth.ErrCodeReused,
			"the loser must lose because the code was already spent")
	}
	require.Equal(t, 1, succeeded, "one code, one use")

	// The invariant that matters whatever the two did.
	after, err := s.TOTPEnrolment(ctx, userID)
	require.NoError(t, err)
	require.Equal(t, uint64(now.Unix()/30), after.LastUsedCounter)
}

// 20.
func TestReEnrolmentResetsConfirmationAndCounterTogether(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	a := auth.DefaultAuthenticator()

	userID, oldSecret, at := enrolled(t, s)

	before, err := s.TOTPEnrolment(ctx, userID)
	require.NoError(t, err)
	require.True(t, before.Confirmed())
	require.NotZero(t, before.LastUsedCounter)

	newSecret, err := auth.NewSecret()
	require.NoError(t, err)
	require.NotEqual(t, oldSecret, newSecret)
	require.NoError(t, s.BeginTOTPEnrolment(ctx, userID, newSecret, at.Add(time.Hour)))

	after, err := s.TOTPEnrolment(ctx, userID)
	require.NoError(t, err)
	require.Equal(t, newSecret, after.Secret)
	require.False(t, after.Confirmed(), "a replaced secret has not been proved")
	require.Zero(t, after.LastUsedCounter,
		"a counter from the old secret means nothing against the new one")

	// The old secret stops working entirely, which is what makes re-enrolment
	// a way to recover from a lost phone rather than a way to have two.
	stale, err := a.Code(oldSecret, at.Add(time.Hour))
	require.NoError(t, err)
	require.ErrorIs(t, s.ConfirmTOTPEnrolment(ctx, a, userID, stale, at.Add(time.Hour)),
		auth.ErrInvalidCode)

	t.Run("disabling", func(t *testing.T) {
		require.NoError(t, s.DisableTOTP(ctx, userID))
		_, err := s.TOTPEnrolment(ctx, userID)
		require.ErrorIs(t, err, auth.ErrNoTOTPEnrolment)
		require.ErrorIs(t, s.DisableTOTP(ctx, userID), auth.ErrNoTOTPEnrolment)
	})
}

// 21, 22, 24.
func TestBackupCodesAreStoredHashedAndSpentOnce(t *testing.T) {
	ctx := context.Background()
	s := open(t)

	userID, at := registerUser(t, s, "person", testEmail)

	codes, err := auth.NewBackupCodes()
	require.NoError(t, err)
	require.NoError(t, s.ReplaceBackupCodes(ctx, userID, codes, at))

	n, err := s.UnusedBackupCodeCount(ctx, userID)
	require.NoError(t, err)
	require.Equal(t, auth.BackupCodeCount, n)

	t.Run("the code itself is nowhere in the table", func(t *testing.T) {
		// What an attacker with a dump would try. The stored form is a hex
		// digest; the code, in any transcription, must not appear.
		for _, written := range []string{
			codes[0],
			strings.ReplaceAll(codes[0], "-", ""),
			strings.ToLower(codes[0]),
		} {
			var matches int
			require.NoError(t, s.Pool().QueryRow(ctx,
				`SELECT count(*) FROM user_backup_codes WHERE code_hash LIKE '%' || $1 || '%'`,
				written).Scan(&matches))
			require.Zero(t, matches, "found %q stored in clear", written)
		}

		var stored string
		require.NoError(t, s.Pool().QueryRow(ctx,
			`SELECT code_hash FROM user_backup_codes ORDER BY code_hash LIMIT 1`).Scan(&stored))
		require.Len(t, stored, 64)
		require.Regexp(t, `^[0-9a-f]{64}$`, stored)
	})

	t.Run("single use", func(t *testing.T) {
		used := at.Add(time.Hour)
		require.NoError(t, s.ConsumeBackupCode(ctx, userID, codes[3], used))

		n, err := s.UnusedBackupCodeCount(ctx, userID)
		require.NoError(t, err)
		require.Equal(t, auth.BackupCodeCount-1, n)

		require.ErrorIs(t,
			s.ConsumeBackupCode(ctx, userID, codes[3], used.Add(time.Minute)),
			auth.ErrInvalidBackupCode)

		// Spent, not deleted, so it can be reported.
		var usedAt *time.Time
		require.NoError(t, s.Pool().QueryRow(ctx,
			`SELECT used_at FROM user_backup_codes WHERE user_id = $1 AND code_hash = $2`,
			userID, auth.HashBackupCode(codes[3])).Scan(&usedAt))
		require.NotNil(t, usedAt)
		require.Equal(t, used.UTC(), usedAt.UTC())

		// Every other code still works, and each is its own.
		require.NoError(t, s.ConsumeBackupCode(ctx, userID, codes[0], used.Add(time.Minute)))
		require.NoError(t, s.ConsumeBackupCode(ctx, userID, codes[9], used.Add(2*time.Minute)))
	})

	t.Run("transcribed by hand", func(t *testing.T) {
		written := strings.ToLower(strings.ReplaceAll(codes[5], "-", " "))
		require.NoError(t, s.ConsumeBackupCode(ctx, userID, written, at.Add(2*time.Hour)))
	})

	t.Run("a code from nowhere", func(t *testing.T) {
		other, err := auth.NewBackupCodes()
		require.NoError(t, err)
		require.ErrorIs(t,
			s.ConsumeBackupCode(ctx, userID, other[0], at.Add(3*time.Hour)),
			auth.ErrInvalidBackupCode)
	})

	t.Run("regenerating discards the previous set entirely", func(t *testing.T) {
		fresh, err := auth.NewBackupCodes()
		require.NoError(t, err)
		require.NoError(t, s.ReplaceBackupCodes(ctx, userID, fresh, at.Add(4*time.Hour)))

		n, err := s.UnusedBackupCodeCount(ctx, userID)
		require.NoError(t, err)
		require.Equal(t, auth.BackupCodeCount, n, "a full new set, not a set plus leftovers")

		// An unspent code from the old list must not survive. Somebody
		// regenerating has usually decided the old list leaked.
		require.ErrorIs(t,
			s.ConsumeBackupCode(ctx, userID, codes[1], at.Add(5*time.Hour)),
			auth.ErrInvalidBackupCode)

		var rows int
		require.NoError(t, s.Pool().QueryRow(ctx,
			`SELECT count(*) FROM user_backup_codes WHERE user_id = $1`, userID).Scan(&rows))
		require.Equal(t, auth.BackupCodeCount, rows,
			"spent rows from the old set must go too, or the table grows forever")

		require.NoError(t, s.ConsumeBackupCode(ctx, userID, fresh[2], at.Add(5*time.Hour)))
	})
}

// 23. The same arrangement as the TOTP race, and a different mechanism under it.
//
// There is no row lock on the backup code path. What this proves is the
// `used_at IS NULL` clause on the UPDATE: both submissions reach it, one
// updates a row and the other updates nothing. Removing the row lock leaves
// this test green — which is how it was established that the lock was not the
// thing working — while removing that clause turns it red.
func TestTwoSubmissionsOfOneBackupCodeCannotBothSucceed(t *testing.T) {
	ctx := context.Background()
	s := open(t)

	userID, at := registerUser(t, s, "person", testEmail)
	codes, err := auth.NewBackupCodes()
	require.NoError(t, err)
	require.NoError(t, s.ReplaceBackupCodes(ctx, userID, codes, at))

	// The gate holds the rows so both submissions are stopped at the same
	// point. It is the test that takes the lock; the code under test does not.
	gate, err := s.Pool().Begin(ctx)
	require.NoError(t, err)
	_, err = gate.Exec(ctx,
		`SELECT code_hash FROM user_backup_codes WHERE user_id = $1 AND used_at IS NULL FOR UPDATE`,
		userID)
	require.NoError(t, err)

	started := make(chan struct{}, 2)
	errs := make([]error, 2)
	var wg sync.WaitGroup
	wg.Add(2)
	for i := range 2 {
		go func() {
			defer wg.Done()
			started <- struct{}{}
			errs[i] = s.ConsumeBackupCode(ctx, userID, codes[4], at.Add(time.Hour))
		}()
	}

	<-started
	<-started
	time.Sleep(300 * time.Millisecond)
	require.NoError(t, gate.Rollback(ctx))
	wg.Wait()

	succeeded := 0
	for _, err := range errs {
		if err == nil {
			succeeded++
			continue
		}
		require.ErrorIs(t, err, auth.ErrInvalidBackupCode,
			"the loser must lose because the code was already spent")
	}
	require.Equal(t, 1, succeeded, "a single-use code used once")

	n, err := s.UnusedBackupCodeCount(ctx, userID)
	require.NoError(t, err)
	require.Equal(t, auth.BackupCodeCount-1, n, "exactly one code was spent")
}

// 25. The column migration 9 reshaped.
func TestAnIdempotentResponseRoundTripsWholeOrNotAtAll(t *testing.T) {
	ctx := context.Background()
	s := open(t)

	require.NoError(t, s.SaveUser(ctx, testID("actor")))
	actor := testID("actor")

	_, err := s.Pool().Exec(ctx,
		`INSERT INTO idempotency_keys (actor_id, key, fingerprint, entity_kind, entity_id, created_at, expires_at)
		 VALUES ($1, 'k', decode(repeat('00', 32), 'hex'), 'transaction', $2, now(), now() + interval '1 day')`,
		actor, testID("entity"))
	require.NoError(t, err)

	t.Run("a live claim carries no response yet", func(t *testing.T) {
		var status *int16
		var body *string
		require.NoError(t, s.Pool().QueryRow(ctx,
			`SELECT response_status, response_body::text FROM idempotency_keys WHERE actor_id = $1 AND key = 'k'`,
			actor).Scan(&status, &body))
		require.Nil(t, status)
		require.Nil(t, body)
	})

	t.Run("both together round-trip", func(t *testing.T) {
		_, err := s.Pool().Exec(ctx,
			`UPDATE idempotency_keys SET response_status = 201, response_body = $2::jsonb
			  WHERE actor_id = $1 AND key = 'k'`,
			actor, `{"id":"x","amount":"1500000"}`)
		require.NoError(t, err)

		var status int16
		var body string
		require.NoError(t, s.Pool().QueryRow(ctx,
			`SELECT response_status, response_body::text FROM idempotency_keys WHERE actor_id = $1 AND key = 'k'`,
			actor).Scan(&status, &body))
		require.Equal(t, int16(201), status,
			"the status is what a body alone could never reproduce")
		require.JSONEq(t, `{"id":"x","amount":"1500000"}`, body)
	})

	for name, sql := range map[string]string{
		"a body with no status": `UPDATE idempotency_keys SET response_status = NULL, response_body = '{}'::jsonb
		                           WHERE actor_id = $1 AND key = 'k'`,
		"a status with no body": `UPDATE idempotency_keys SET response_status = 200, response_body = NULL
		                           WHERE actor_id = $1 AND key = 'k'`,
		"not an http status": `UPDATE idempotency_keys SET response_status = 42, response_body = '{}'::jsonb
		                        WHERE actor_id = $1 AND key = 'k'`,
	} {
		t.Run(name+" is refused", func(t *testing.T) {
			_, err := s.Pool().Exec(ctx, sql, actor)
			require.Error(t, err, "half a response would make a replay invent the other half")
		})
	}
}

// The real implementation behind the audit events the HTTP layer records. The
// handler tests use a fake, so without this the only thing exercising
// Store.RecordEvent would be nothing at all.
func TestAuthEventsReachTheAuditLog(t *testing.T) {
	ctx := context.Background()
	s := open(t)

	userID, at := registerUser(t, s, "person", testEmail)

	require.NoError(t, s.RecordEvent(ctx, auth.Event{
		ID: testID("event-1"), ActorID: userID, At: at,
		Action: "sign_in", EntityKind: "session", EntityID: testID("session-1"),
	}))
	require.NoError(t, s.RecordEvent(ctx, auth.Event{
		ID: testID("event-2"), ActorID: userID, At: at.Add(time.Hour),
		Action: "sign_out", EntityKind: "session", EntityID: testID("session-1"),
		Detail: []byte(`{"reason":"user requested"}`),
	}))

	entries, err := s.AuditEntriesFor(ctx, "session", testID("session-1"))
	require.NoError(t, err)
	require.Len(t, entries, 2)

	// Newest first, which is what a history view asks for.
	require.Equal(t, "sign_out", entries[0].Action)
	require.Equal(t, "sign_in", entries[1].Action)
	for _, e := range entries {
		require.Equal(t, userID, e.ActorID)
		require.Equal(t, store.OriginHuman, e.Origin,
			"nothing but a person signs in, so an auth event is always a human origin")
	}
	require.JSONEq(t, `{"reason":"user requested"}`, string(entries[0].Diff))
	require.JSONEq(t, `{}`, string(entries[1].Diff),
		"an event with no detail still stores valid JSON, because the column is NOT NULL")

	t.Run("an event without an actor is refused", func(t *testing.T) {
		// audit_log_human_origin_has_an_actor is what makes the rest of the log
		// trustworthy, and it is why anonymous sign-in attempts are not written
		// here at all — see the note on auth.Event.
		err := s.RecordEvent(ctx, auth.Event{
			ID: testID("event-3"), At: at, Action: "sign_in",
			EntityKind: "session", EntityID: testID("session-1"),
		})
		require.ErrorIs(t, err, store.ErrInvalidWrite)
	})

	t.Run("an event without an action is refused", func(t *testing.T) {
		err := s.RecordEvent(ctx, auth.Event{
			ID: testID("event-4"), ActorID: userID, At: at,
			EntityKind: "session", EntityID: testID("session-1"),
		})
		require.ErrorIs(t, err, store.ErrInvalidWrite)
	})
}
