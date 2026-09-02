// SPDX-License-Identifier: AGPL-3.0-only

package auth_test

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/powfulf/Nusa/internal/auth"
)

// What these guards must cover. Written before they were, and each item was
// broken on purpose and watched failing afterwards, against a prediction
// written first (CLAUDE.md §11).
//
//	 1. Below the limit an attempt is allowed; at the limit it is refused.
//	 2. Allow records nothing — checking is not spending.
//	 3. retryAfter counts down to the oldest failure leaving the window.
//	 4. The window slides: once the oldest failure expires, one slot returns.
//	 5. A success clears the count.
//	 6. Keys are independent, which is what stops one person locking out
//	    another.
//	 7. Continuing to fail while blocked does not extend the block.
//	 8. Sweeping drops what has expired and keeps what has not.
//	 9. Key cardinality is bounded, and the bound fails open rather than shut.
//	10. Unusable parameters are refused.
//	11. Concurrent use is safe.

const t0 = 1_700_000_000

func at(seconds int) time.Time { return time.Unix(t0+int64(seconds), 0).UTC() }

func newLimiter(t *testing.T, limit int, window time.Duration) *auth.Limiter {
	t.Helper()
	l, err := auth.NewLimiter(limit, window)
	require.NoError(t, err)
	return l
}

// 1, 2, 3.
func TestFailuresAccumulateUntilTheLimit(t *testing.T) {
	t.Parallel()
	l := newLimiter(t, 3, time.Minute)

	// Checking does not spend. A handler may ask several times before it ever
	// records anything, and asking must not itself use the allowance.
	for range 10 {
		retry, ok := l.Allow("addr-a", at(0))
		require.True(t, ok)
		require.Zero(t, retry)
	}

	l.Fail("addr-a", at(0))
	l.Fail("addr-a", at(10))
	_, ok := l.Allow("addr-a", at(11))
	require.True(t, ok, "two failures out of three still leaves one attempt")

	l.Fail("addr-a", at(20))
	retry, ok := l.Allow("addr-a", at(21))
	require.False(t, ok, "the third failure reaches the limit")

	// The wait is until the *oldest* failure leaves the window, not the whole
	// window. The oldest is at t=0, the window is 60s, so at t=21 that is 39s.
	require.Equal(t, 39*time.Second, retry)
}

// 4.
func TestTheWindowSlides(t *testing.T) {
	t.Parallel()
	l := newLimiter(t, 2, time.Minute)

	l.Fail("addr-a", at(0))
	l.Fail("addr-a", at(30))
	_, ok := l.Allow("addr-a", at(31))
	require.False(t, ok)

	// One second before the oldest expires.
	_, ok = l.Allow("addr-a", at(59))
	require.False(t, ok, "the failure at t=0 is still inside a 60s window at t=59")

	// Exactly one window later it has expired, freeing its slot.
	_, ok = l.Allow("addr-a", at(60))
	require.True(t, ok, "a failure exactly one window old has left the window")

	// But only one slot: the failure at t=30 is still live.
	l.Fail("addr-a", at(60))
	_, ok = l.Allow("addr-a", at(61))
	require.False(t, ok)

	// Past both, everything is clear.
	_, ok = l.Allow("addr-a", at(121))
	require.True(t, ok)
}

// 5.
func TestASuccessClearsTheCount(t *testing.T) {
	t.Parallel()
	l := newLimiter(t, 3, time.Minute)

	l.Fail("addr-a", at(0))
	l.Fail("addr-a", at(1))
	l.Succeed("addr-a")

	// Somebody who mistyped twice and then got it right is not left one
	// attempt from being locked out of their own instance.
	_, ok := l.Allow("addr-a", at(2))
	require.True(t, ok)
	require.Zero(t, l.TrackedKeys(), "a cleared key is forgotten, not merely reset")

	for i := range 3 {
		l.Fail("addr-a", at(3+i))
	}
	_, ok = l.Allow("addr-a", at(10))
	require.False(t, ok, "the allowance is genuinely full again afterwards")
}

// 6. The property that decides the whole design.
//
// The key is the client address and the account is never part of it. A limiter
// keyed on the account would let anyone who can reach the login form lock a
// named person out by sending wrong passwords for their address — and on a
// self-hosted instance with one account, that is the entire service.
//
// There is no API here that accepts an email, so the strongest available
// statement is this one: exhausting one key leaves every other key untouched.
func TestOneKeyBeingBlockedDoesNotAffectAnother(t *testing.T) {
	t.Parallel()
	l := newLimiter(t, 3, time.Minute)

	attacker, victim := "203.0.113.9", "198.51.100.4"

	for i := range 20 {
		l.Fail(attacker, at(i))
	}
	_, ok := l.Allow(attacker, at(21))
	require.False(t, ok, "the attacker's own address is blocked")

	// The person being targeted signs in from somewhere else and is unaffected.
	retry, ok := l.Allow(victim, at(21))
	require.True(t, ok, "an attacker must not be able to spend somebody else's allowance")
	require.Zero(t, retry)

	// And their own mistakes still count normally.
	l.Fail(victim, at(21))
	_, ok = l.Allow(victim, at(22))
	require.True(t, ok)
}

// 7.
func TestFailingWhileBlockedDoesNotExtendTheBlock(t *testing.T) {
	t.Parallel()
	l := newLimiter(t, 3, time.Minute)

	for i := range 3 {
		l.Fail("addr-a", at(i))
	}
	_, blocked := l.Allow("addr-a", at(3))
	require.False(t, blocked)

	// A script that keeps hammering during the block must not push its own
	// release further out. If it could, an attacker could hold an address
	// blocked forever — and for a NAT gateway that address is a household.
	for i := 3; i < 60; i++ {
		l.Fail("addr-a", at(i))
	}

	retry, ok := l.Allow("addr-a", at(59))
	require.False(t, ok)
	require.Equal(t, time.Second, retry,
		"release is still driven by the failure at t=0, not by the flood on top of it")

	_, ok = l.Allow("addr-a", at(60))
	require.True(t, ok, "the block expires on schedule despite the flood")

	// The log stays bounded at the limit however long the flood runs, which is
	// what keeps the memory cost of an attack independent of its duration.
	for i := 60; i < 400; i++ {
		l.Fail("addr-a", at(i))
	}
	require.Equal(t, 1, l.TrackedKeys())

	// And slots keep coming back. A limiter that stayed blocked for as long as
	// an attacker kept trying would give anyone sharing that address — the
	// other people behind one NAT gateway — no opportunity at all. Here the
	// allowance refills every window, so the attacker is held to limit-per-
	// window and a neighbour still gets openings.
	opportunities := 0
	for i := 400; i < 700; i++ {
		if _, ok := l.Allow("addr-a", at(i)); ok {
			opportunities++
		}
		l.Fail("addr-a", at(i))
	}
	require.NotZero(t, opportunities,
		"a continuously flooded key must still see the allowance refill")
	t.Logf("under continuous failure, %d of 300 seconds allowed an attempt", opportunities)
}

// 8.
func TestSweepingDropsWhatHasExpired(t *testing.T) {
	t.Parallel()
	l := newLimiter(t, 5, time.Minute)

	l.Fail("old", at(0))
	l.Fail("new", at(50))
	require.Equal(t, 2, l.TrackedKeys())

	dropped := l.Sweep(at(61))
	require.Equal(t, 1, dropped)
	require.Equal(t, 1, l.TrackedKeys(), "the recent key survives")

	require.Equal(t, 1, l.Sweep(at(200)))
	require.Zero(t, l.TrackedKeys())

	// Sweeping is an optimisation, never a correctness requirement: a limiter
	// that is never swept still answers correctly, because every read filters.
	l2 := newLimiter(t, 1, time.Minute)
	l2.Fail("addr-a", at(0))
	_, ok := l2.Allow("addr-a", at(120))
	require.True(t, ok, "an unswept expired failure must not still block")
}

// 9. Under a flood of distinct addresses the map is bounded, and the bound
// releases allowances rather than withholding them.
//
// Failing open is the deliberate direction. Failing closed under memory
// pressure would mean an attacker with enough source addresses could block
// every legitimate user at once — which is the same lockout this design
// rejected when it declined to key on the account.
func TestKeyCardinalityIsBoundedAndFailsOpen(t *testing.T) {
	t.Parallel()
	l := newLimiter(t, 5, time.Hour)

	const flood = 25_000
	for i := range flood {
		l.Fail(fmt.Sprintf("10.%d.%d.%d", i>>16&0xff, i>>8&0xff, i&0xff), at(0))
	}

	tracked := l.TrackedKeys()
	require.Less(t, tracked, flood,
		"the map grew with the flood, so an attacker chooses how much memory this uses")
	t.Logf("after %d distinct keys, %d tracked", flood, tracked)

	// Failing open: a fresh key is served, it is not refused because the
	// limiter is full.
	retry, ok := l.Allow("198.51.100.4", at(0))
	require.True(t, ok, "a legitimate address must not be blocked by somebody else's flood")
	require.Zero(t, retry)
}

// 10.
func TestUnusableLimiterParametersAreRefused(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		limit  int
		window time.Duration
	}{
		"zero limit":      {0, time.Minute},
		"negative limit":  {-1, time.Minute},
		"zero window":     {5, 0},
		"negative window": {5, -time.Minute},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := auth.NewLimiter(tc.limit, tc.window)
			require.ErrorIs(t, err, auth.ErrInvalidParameters)
		})
	}

	limit, window := auth.LimiterDefaults()
	require.Positive(t, limit)
	require.Positive(t, window)
	_, err := auth.NewLimiter(limit, window)
	require.NoError(t, err, "LimiterDefaults must itself be acceptable")
}

// 11. Run under -race, which is where this earns its place.
func TestConcurrentUseIsSafe(t *testing.T) {
	t.Parallel()
	l := newLimiter(t, 5, time.Minute)

	var wg sync.WaitGroup
	for i := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			key := fmt.Sprintf("addr-%d", i%3)
			for j := range 200 {
				l.Allow(key, at(j))
				l.Fail(key, at(j))
				if j%50 == 0 {
					l.Succeed(key)
					l.Sweep(at(j))
				}
			}
		}()
	}
	wg.Wait()
}
