// SPDX-License-Identifier: AGPL-3.0-only

package auth

import (
	"fmt"
	"sync"
	"time"
)

// Rate limiting for sign-in attempts.
//
// This lives in the pure package rather than beside the HTTP handler because
// the decision is policy, not transport: how many failures are too many, over
// what span, and what resets the count. Nothing here reads a request, a header
// or a clock — the instant is passed in — so the whole of it is testable
// without a server and without a container.
//
// # What is counted, and what is not
//
// **Failed sign-in attempts**, not requests. A limiter wired into middleware
// counts everything that reaches a URL, which throttles a browser reloading a
// login page and does nothing whatever about credential stuffing sent as a
// tight loop of well-formed POSTs. The caller records a failure only when a
// credential was actually presented and actually rejected.
//
// A successful sign-in clears the count. Somebody who mistypes a password four
// times and then gets it right is not left one attempt away from being locked
// out of their own instance for the rest of the window.
//
// # Why the key is the client address and never the account
//
// This is the decision most worth arguing about, so here is the argument.
//
// Counting failures per account is the obvious way to stop a single account
// being brute-forced, and it hands anyone who can reach the login form a way to
// lock a named person out: send wrong passwords for their address until the
// limit trips, repeat forever. The victim has done nothing and can do nothing.
//
// For Nusa that is not a trade-off between two comparable harms, because Nusa
// is self-hosted and — until M9 brings households — holds exactly one account.
// A per-account limiter is therefore a per-instance limiter, and any stranger
// who can reach the login page can deny the only user access to their own
// finances indefinitely. That is a worse outcome, more easily reached, than the
// brute force it would be defending against.
//
// So the key is the client address, and the account never enters it.
//
// What that costs: an attacker with many source addresses is not slowed by this
// limiter. What makes that acceptable is that the limiter was never the main
// defence against guessing — Argon2id is. At the cost measured in
// DefaultParams, a server answers on the order of a few attempts a second even
// with no limiter at all, and against any password that is not in the first
// thousand guesses that rate is nothing. The limiter exists to stop one source
// from making that small rate matter, and to keep a scripted flood from
// consuming a self-hosted machine's memory in Argon2id calls.
//
// It also does not defend a *weak* password, and nothing at this layer can. An
// attacker distributing guesses across a botnet defeats per-address limiting —
// but they defeat per-account limiting too, by locking the account instead,
// which leaves the user worse off than being merely attacked.
//
// # What keying on the address costs instead
//
// It is not free, and the cost lands on people sharing an address. Everyone
// behind one NAT gateway — a household, an office, a phone on carrier-grade
// NAT — shares one allowance, so somebody else's failures can consume it.
//
// That is bounded rather than open-ended, and deliberately so. A key at its
// limit does not record further failures (see Fail), so a flood cannot hold an
// address blocked indefinitely: the allowance refills every window regardless
// of how long the attempt goes on, and a co-located user keeps getting
// openings. Measured over a continuous flood, exactly `limit` attempts per
// window come back.
//
// The alternative shape — extending the block for as long as failures keep
// arriving — would punish that co-located user without bounding the attacker
// any further, since the attacker was already held to limit-per-window.
//
// This is a deliberate choice with a known gap, recorded rather than decided
// quietly. If Nusa ever grows many accounts per instance, the shape to revisit
// is a per-account counter that *slows* rather than blocks, never one that
// locks.

// Limiter counts recent failures per key and decides whether another attempt
// may proceed.
//
// The zero value is not usable; call NewLimiter.
type Limiter struct {
	limit  int
	window time.Duration

	// maxKeys bounds memory. See the note on eviction in Fail.
	maxKeys int

	mu sync.Mutex
	// failures holds, per key, the instants of recent failures. A sliding log
	// rather than a fixed window: a fixed window lets 2×limit attempts through
	// across a boundary, which is exactly the burst an attacker would aim for,
	// and at these sizes the log is at most `limit` timestamps per key.
	failures map[string][]time.Time
}

// LimiterDefaults returns the policy Nusa applies to sign-in attempts.
//
// Ten failures in fifteen minutes. The number is chosen from what a person
// actually does rather than from what an attacker needs: somebody reaching for
// a password manager, trying an old password, then a variant, then the right
// one, is three or four attempts. Ten leaves room for that twice over and still
// bounds a scripted attempt to forty a hour from one address, against which any
// password with real entropy is untouched.
//
// Fifteen minutes rather than an hour because the penalty falls on people who
// mistyped, and an hour locked out of your own finances over a typo is a
// punishment out of proportion to what it prevents.
func LimiterDefaults() (limit int, window time.Duration) {
	return 10, 15 * time.Minute
}

// defaultMaxKeys bounds how many distinct addresses are tracked at once.
//
// Ten thousand is far more than a household instance sees and small enough to
// be irrelevant to memory: at the limit above it is at most 100k timestamps.
// The bound matters because the map is keyed by something an attacker
// influences — see Fail for what happens when it is reached, and why that
// direction was chosen.
const defaultMaxKeys = 10_000

// NewLimiter returns a Limiter allowing limit failures per key per window.
func NewLimiter(limit int, window time.Duration) (*Limiter, error) {
	switch {
	case limit < 1:
		return nil, fmt.Errorf("%w: limit must be at least 1, got %d", ErrInvalidParameters, limit)
	case window <= 0:
		return nil, fmt.Errorf("%w: window must be positive, got %s", ErrInvalidParameters, window)
	}
	return &Limiter{
		limit:    limit,
		window:   window,
		maxKeys:  defaultMaxKeys,
		failures: make(map[string][]time.Time),
	}, nil
}

// Allow reports whether another attempt may be made for key, and if not, how
// long until one may.
//
// It records nothing. Checking and recording are separate on purpose: the check
// happens before an Argon2id verification so a blocked attempt costs nothing,
// and the recording happens after, only if the credential was wrong.
func (l *Limiter) Allow(key string, now time.Time) (retryAfter time.Duration, ok bool) {
	l.mu.Lock()
	defer l.mu.Unlock()

	recent := l.recentLocked(key, now)
	if len(recent) < l.limit {
		return 0, true
	}

	// The oldest failure still inside the window is the one whose expiry frees
	// a slot. Reporting that rather than the whole window means a caller can
	// tell someone how long to wait and be right.
	return l.window - now.Sub(recent[0]), false
}

// Fail records a rejected sign-in attempt for key.
func (l *Limiter) Fail(key string, now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()

	recent := l.recentLocked(key, now)
	if len(recent) >= l.limit {
		// Already at the limit. Not appending keeps the log bounded at exactly
		// limit entries per key and stops a flood from extending its own
		// blocked period indefinitely, which would turn the limiter into a
		// self-inflicted lockout of that address.
		l.failures[key] = recent
		return
	}
	l.failures[key] = append(recent, now)

	if len(l.failures) > l.maxKeys {
		l.sweepLocked(now)
		// Still over after sweeping means genuinely many live addresses. The
		// map is emptied rather than allowed to grow without bound, which
		// fails *open*: those addresses get a fresh allowance. That direction
		// is deliberate — failing closed under memory pressure would mean an
		// attacker with enough source addresses could lock out every
		// legitimate user at once, which is the outcome this whole file is
		// arranged to avoid.
		if len(l.failures) > l.maxKeys {
			l.failures = make(map[string][]time.Time)
		}
	}
}

// Succeed clears the count for key after a successful sign-in.
func (l *Limiter) Succeed(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.failures, key)
}

// Sweep discards expired entries and reports how many keys it dropped. A
// caller runs it periodically; nothing here depends on it for correctness,
// because recentLocked filters on every read.
func (l *Limiter) Sweep(now time.Time) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.sweepLocked(now)
}

// TrackedKeys reports how many keys are currently held. It exists for tests
// and for an operator metric, and is not part of any decision.
func (l *Limiter) TrackedKeys() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.failures)
}

// recentLocked returns the failures for key that are still inside the window,
// oldest first, and stores the trimmed slice back.
func (l *Limiter) recentLocked(key string, now time.Time) []time.Time {
	stored, found := l.failures[key]
	if !found {
		return nil
	}

	cutoff := now.Add(-l.window)
	keep := stored[:0]
	for _, at := range stored {
		// After, not !Before: a failure exactly one window old has expired.
		// The boundary has to fall one way and the generous reading is the one
		// that does not hold somebody an extra tick over an off-by-one.
		if at.After(cutoff) {
			keep = append(keep, at)
		}
	}
	if len(keep) == 0 {
		delete(l.failures, key)
		return nil
	}
	l.failures[key] = keep
	return keep
}

func (l *Limiter) sweepLocked(now time.Time) int {
	before := len(l.failures)
	for key := range l.failures {
		l.recentLocked(key, now)
	}
	return before - len(l.failures)
}
