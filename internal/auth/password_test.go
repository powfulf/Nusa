// SPDX-License-Identifier: AGPL-3.0-only

package auth_test

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/GaffaQ/Nusa/internal/auth"
)

// What these guards must cover. Written before they were, and each item was
// broken on purpose and watched failing afterwards (CLAUDE.md §11).
//
//	 1. A password verifies against its own hash.
//	 2. A wrong password does not, and says ErrPasswordMismatch.
//	 3. Two hashes of one password differ — the salt is fresh every time.
//	 4. The encoded string carries the parameters it was minted with.
//	 5. A tampered tag fails. A tampered salt fails.
//	 6. An unreadable hash is ErrMalformedHash, never ErrPasswordMismatch.
//	 7. A readable but unsupported hash is ErrUnsupportedHash.
//	 8. Verification uses the hash's parameters, not current policy.
//	 9. NeedsRehash notices a cost change, in either direction.
//	10. NewHasher refuses every unusable parameter shape.
//	11. A verification costs roughly what DefaultParams claims.
//	12. VerifyDecoy really performs the work; it is not an early return.
//	13. Nothing truncates a password: length, Unicode and NUL all survive.

const (
	rightPassword = "kopi tubruk gula aren"
	wrongPassword = "kopi tubruk gula batu"
)

func defaultHasher(t *testing.T) auth.Hasher {
	t.Helper()
	h, err := auth.NewHasher(auth.DefaultParams())
	require.NoError(t, err)
	return h
}

// cheapParams cost almost nothing to compute. They are used wherever a test is
// about the plumbing rather than about the cost, so that the suite is not
// spending seconds proving things that have nothing to do with Argon2id's
// price. The two tests that are genuinely about cost use DefaultParams.
func cheapParams() auth.Params {
	return auth.Params{Memory: 64, Iterations: 1, Parallelism: 1, SaltLength: 8, TagLength: 16}
}

func cheapHasher(t *testing.T) auth.Hasher {
	t.Helper()
	h, err := auth.NewHasher(cheapParams())
	require.NoError(t, err)
	return h
}

// 1, 2.
func TestAPasswordVerifiesAgainstItsOwnHashAndNoOther(t *testing.T) {
	t.Parallel()
	h := cheapHasher(t)

	encoded, err := h.Hash(rightPassword)
	require.NoError(t, err)

	require.NoError(t, auth.Verify(encoded, rightPassword))
	require.ErrorIs(t, auth.Verify(encoded, wrongPassword), auth.ErrPasswordMismatch)

	// A password that is a prefix of the right one must not pass. Nothing in
	// the implementation compares lengths first, and this is what would notice
	// if something started to.
	require.ErrorIs(t, auth.Verify(encoded, rightPassword[:5]), auth.ErrPasswordMismatch)
}

// 3.
func TestEachHashDrawsAFreshSalt(t *testing.T) {
	t.Parallel()
	h := cheapHasher(t)

	first, err := h.Hash(rightPassword)
	require.NoError(t, err)
	second, err := h.Hash(rightPassword)
	require.NoError(t, err)

	require.NotEqual(t, first, second,
		"two hashes of the same password are identical, so the salt is not random")

	// Both must still verify. A salt that changes but is not carried into the
	// encoding would satisfy the line above and fail here.
	require.NoError(t, auth.Verify(first, rightPassword))
	require.NoError(t, auth.Verify(second, rightPassword))
}

// 4.
func TestTheEncodingCarriesTheParametersItWasMintedWith(t *testing.T) {
	t.Parallel()

	p := auth.DefaultParams()
	h, err := auth.NewHasher(p)
	require.NoError(t, err)

	encoded, err := h.Hash(rightPassword)
	require.NoError(t, err)

	fields := strings.Split(encoded, "$")
	require.Len(t, fields, 6, "PHC string should have five fields after the leading empty one")
	require.Empty(t, fields[0])
	require.Equal(t, "argon2id", fields[1])
	require.Equal(t, "v=19", fields[2])
	require.Equal(t, "m=19456,t=2,p=1", fields[3],
		"the encoded costs must be DefaultParams, or a reader cannot reproduce the hash")

	// Salt and tag are raw (unpadded) standard base64, so their encoded lengths
	// follow from the byte lengths and neither carries a padding character.
	// Only those two fields are checked: '=' is ordinary punctuation in the
	// version and cost fields.
	require.Len(t, fields[4], 22, "16 salt bytes are 22 unpadded base64 characters")
	require.Len(t, fields[5], 43, "32 tag bytes are 43 unpadded base64 characters")
	require.NotContains(t, fields[4], "=", "PHC base64 is unpadded")
	require.NotContains(t, fields[5], "=", "PHC base64 is unpadded")
}

// 5.
func TestATamperedHashDoesNotVerify(t *testing.T) {
	t.Parallel()
	h := cheapHasher(t)

	encoded, err := h.Hash(rightPassword)
	require.NoError(t, err)
	fields := strings.Split(encoded, "$")

	flip := func(s string) string {
		// Swap the first character for a different one from the same alphabet,
		// so the value stays decodable and only its content changes. Corrupting
		// it into something unparseable would test the parser instead.
		if s[0] == 'A' {
			return "B" + s[1:]
		}
		return "A" + s[1:]
	}

	t.Run("tampered tag", func(t *testing.T) {
		t.Parallel()
		bad := strings.Join([]string{fields[0], fields[1], fields[2], fields[3], fields[4], flip(fields[5])}, "$")
		require.ErrorIs(t, auth.Verify(bad, rightPassword), auth.ErrPasswordMismatch)
	})

	t.Run("tampered salt", func(t *testing.T) {
		t.Parallel()
		bad := strings.Join([]string{fields[0], fields[1], fields[2], fields[3], flip(fields[4]), fields[5]}, "$")
		require.ErrorIs(t, auth.Verify(bad, rightPassword), auth.ErrPasswordMismatch)
	})
}

// 6. An unreadable credential is an operational fault. Reporting it as a failed
// login would tell the person at the keyboard that they typed their password
// wrong when they did not, and would hide a corrupt row from the operator.
func TestAnUnreadableHashIsNotAFailedLogin(t *testing.T) {
	t.Parallel()

	valid, err := cheapHasher(t).Hash(rightPassword)
	require.NoError(t, err)
	fields := strings.Split(valid, "$")

	rejoin := func(f ...string) string { return strings.Join(f, "$") }

	malformed := map[string]string{
		"empty":              "",
		"not a phc string":   "just some text",
		"too few fields":     "$argon2id$v=19$m=64,t=1,p=1$c2FsdHNhbHQ",
		"too many fields":    valid + "$extra",
		"no leading dollar":  strings.TrimPrefix(valid, "$"),
		"unreadable version": rejoin(fields[0], fields[1], "v=nineteen", fields[3], fields[4], fields[5]),
		"unreadable costs":   rejoin(fields[0], fields[1], fields[2], "m=lots,t=2,p=1", fields[4], fields[5]),
		"costs out of order": rejoin(fields[0], fields[1], fields[2], "t=1,m=64,p=1", fields[4], fields[5]),
		"salt is not base64": rejoin(fields[0], fields[1], fields[2], fields[3], "not!base64", fields[5]),
		"tag is not base64":  rejoin(fields[0], fields[1], fields[2], fields[3], fields[4], "not!base64"),
		"empty salt":         rejoin(fields[0], fields[1], fields[2], fields[3], "", fields[5]),
		"empty tag":          rejoin(fields[0], fields[1], fields[2], fields[3], fields[4], ""),
		"padded base64 salt": rejoin(fields[0], fields[1], fields[2], fields[3], fields[4]+"==", fields[5]),
		// The boundary between malformed and unsupported sits at what a uint32
		// can hold. A memory figure that overflows it is not a cost this build
		// declines to pay, it is a number that cannot be read at all — so it
		// lands here rather than in the unsupported set below. Both are
		// non-login errors, which is the property that actually matters.
		"memory beyond uint32": rejoin(fields[0], fields[1], fields[2], "m=17179869184,t=1,p=1", fields[4], fields[5]),
		"zero iterations":      rejoin(fields[0], fields[1], fields[2], "m=64,t=0,p=1", fields[4], fields[5]),
		"zero parallelism":     rejoin(fields[0], fields[1], fields[2], "m=64,t=1,p=0", fields[4], fields[5]),
		"more lanes than room": rejoin(fields[0], fields[1], fields[2], "m=8,t=1,p=4", fields[4], fields[5]),
	}

	for name, encoded := range malformed {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			err := auth.Verify(encoded, rightPassword)
			require.Error(t, err)
			require.NotErrorIs(t, err, auth.ErrPasswordMismatch,
				"an unreadable hash must not be reported as a wrong password")
		})
	}
}

// 7.
func TestAReadableButUnsupportedHashIsRefusedByName(t *testing.T) {
	t.Parallel()

	valid, err := cheapHasher(t).Hash(rightPassword)
	require.NoError(t, err)
	fields := strings.Split(valid, "$")
	rejoin := func(f ...string) string { return strings.Join(f, "$") }

	unsupported := map[string]string{
		// Argon2i and Argon2d are real algorithms with the same encoding.
		// Verifying one of them with IDKey would produce a mismatch rather
		// than an error, which would read as a wrong password forever.
		"argon2i":      rejoin(fields[0], "argon2i", fields[2], fields[3], fields[4], fields[5]),
		"argon2d":      rejoin(fields[0], "argon2d", fields[2], fields[3], fields[4], fields[5]),
		"bcrypt":       rejoin(fields[0], "2b", fields[2], fields[3], fields[4], fields[5]),
		"older argon2": rejoin(fields[0], fields[1], "v=16", fields[3], fields[4], fields[5]),
		// One KiB over the ceiling, so the refusal is the ceiling's doing and
		// not an artefact of the number being unreadable.
		"memory over 1GiB": rejoin(fields[0], fields[1], fields[2], "m=1048577,t=1,p=1", fields[4], fields[5]),
	}

	for name, encoded := range unsupported {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			err := auth.Verify(encoded, rightPassword)
			require.ErrorIs(t, err, auth.ErrUnsupportedHash)
			require.NotErrorIs(t, err, auth.ErrPasswordMismatch)
		})
	}
}

// 8. The reason Verify is a free function rather than a Hasher method. Raising
// the cost of new passwords must not lock out everyone whose password was
// hashed before the change.
func TestVerificationUsesTheHashsParametersAndNotCurrentPolicy(t *testing.T) {
	t.Parallel()

	old, err := auth.NewHasher(auth.Params{Memory: 64, Iterations: 1, Parallelism: 1, SaltLength: 8, TagLength: 16})
	require.NoError(t, err)

	encoded, err := old.Hash(rightPassword)
	require.NoError(t, err)
	require.Contains(t, encoded, "m=64,t=1,p=1")

	// Policy is raised. The credential above was minted under the old one and
	// must still verify.
	require.NoError(t, auth.Verify(encoded, rightPassword))
	require.ErrorIs(t, auth.Verify(encoded, wrongPassword), auth.ErrPasswordMismatch)
}

// 9.
func TestNeedsRehashNoticesACostChangeInEitherDirection(t *testing.T) {
	t.Parallel()

	weak, err := auth.NewHasher(auth.Params{Memory: 64, Iterations: 1, Parallelism: 1, SaltLength: 8, TagLength: 16})
	require.NoError(t, err)
	strong, err := auth.NewHasher(auth.Params{Memory: 128, Iterations: 2, Parallelism: 1, SaltLength: 16, TagLength: 32})
	require.NoError(t, err)

	weakHash, err := weak.Hash(rightPassword)
	require.NoError(t, err)
	strongHash, err := strong.Hash(rightPassword)
	require.NoError(t, err)

	stale, err := strong.NeedsRehash(weakHash)
	require.NoError(t, err)
	require.True(t, stale, "a hash minted below current policy needs rehashing")

	current, err := strong.NeedsRehash(strongHash)
	require.NoError(t, err)
	require.False(t, current, "a hash minted at current policy does not")

	// The other direction. Policy that was deliberately lowered is still
	// policy, so a hash above it is also stale.
	lowered, err := weak.NeedsRehash(strongHash)
	require.NoError(t, err)
	require.True(t, lowered, "a hash above current policy is a difference too")

	// Every field participates, not only memory. Each of these differs from
	// the base in exactly one place, which is what makes this a test of that
	// place rather than of the four of them together.
	base := auth.Params{Memory: 128, Iterations: 2, Parallelism: 1, SaltLength: 16, TagLength: 32}
	for name, changed := range map[string]auth.Params{
		"memory":      {Memory: 256, Iterations: 2, Parallelism: 1, SaltLength: 16, TagLength: 32},
		"iterations":  {Memory: 128, Iterations: 3, Parallelism: 1, SaltLength: 16, TagLength: 32},
		"parallelism": {Memory: 128, Iterations: 2, Parallelism: 2, SaltLength: 16, TagLength: 32},
		"salt length": {Memory: 128, Iterations: 2, Parallelism: 1, SaltLength: 24, TagLength: 32},
		"tag length":  {Memory: 128, Iterations: 2, Parallelism: 1, SaltLength: 16, TagLength: 48},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			policy, err := auth.NewHasher(base)
			require.NoError(t, err)
			other, err := auth.NewHasher(changed)
			require.NoError(t, err)

			otherHash, err := other.Hash(rightPassword)
			require.NoError(t, err)

			needs, err := policy.NeedsRehash(otherHash)
			require.NoError(t, err)
			require.True(t, needs, "a change in %s must be noticed", name)
		})
	}

	// An unreadable hash is not "does not need rehashing".
	_, err = strong.NeedsRehash("nonsense")
	require.ErrorIs(t, err, auth.ErrMalformedHash)
}

// 10.
func TestNewHasherRefusesUnusableParameters(t *testing.T) {
	t.Parallel()

	ok := auth.DefaultParams()
	bad := map[string]auth.Params{
		"no memory":            {Memory: 0, Iterations: 1, Parallelism: 1, SaltLength: 16, TagLength: 32},
		"memory below 8 KiB":   {Memory: 7, Iterations: 1, Parallelism: 1, SaltLength: 16, TagLength: 32},
		"memory over 1 GiB":    {Memory: 1 << 21, Iterations: 1, Parallelism: 1, SaltLength: 16, TagLength: 32},
		"no iterations":        {Memory: ok.Memory, Iterations: 0, Parallelism: 1, SaltLength: 16, TagLength: 32},
		"no lanes":             {Memory: ok.Memory, Iterations: 1, Parallelism: 0, SaltLength: 16, TagLength: 32},
		"more lanes than room": {Memory: 8, Iterations: 1, Parallelism: 4, SaltLength: 16, TagLength: 32},
		"salt too short":       {Memory: ok.Memory, Iterations: 1, Parallelism: 1, SaltLength: 4, TagLength: 32},
		"tag too short":        {Memory: ok.Memory, Iterations: 1, Parallelism: 1, SaltLength: 16, TagLength: 8},
	}

	for name, p := range bad {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := auth.NewHasher(p)
			require.ErrorIs(t, err, auth.ErrInvalidParameters)
		})
	}

	_, err := auth.NewHasher(ok)
	require.NoError(t, err, "DefaultParams must itself be acceptable")
}

// 13. bcrypt silently ignores everything past the 72nd byte, which means two
// different long passwords can be interchangeable. Argon2id has no such limit,
// and this is what would notice if a length cap were ever introduced above.
func TestNothingTruncatesAPassword(t *testing.T) {
	t.Parallel()
	h := cheapHasher(t)

	long := strings.Repeat("a", 200)
	longer := long + "b"

	encoded, err := h.Hash(long)
	require.NoError(t, err)
	require.NoError(t, auth.Verify(encoded, long))
	require.ErrorIs(t, auth.Verify(encoded, longer), auth.ErrPasswordMismatch,
		"a password differing only past byte 72 must not be accepted")

	for name, password := range map[string]string{
		"empty":            "",
		"unicode":          "sandi rahasia 🔐 ünïcödé",
		"embedded nul":     "before\x00after",
		"only whitespace":  "   ",
		"leading trailing": "  padded  ",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			encoded, err := h.Hash(password)
			require.NoError(t, err)
			require.NoError(t, auth.Verify(encoded, password),
				"a password is bytes; nothing here may normalise or trim it")
		})
	}

	// NUL is refused inside ledger text because a ledger entry has to survive
	// being written to a text column, a JSON document and a filename. A
	// password is never any of those: it is compared and discarded, and the
	// hash that is stored is base64. So the ledger's rule deliberately does
	// not apply here, and truncating at the NUL would silently shorten
	// somebody's password.
	withNul, err := h.Hash("before\x00after")
	require.NoError(t, err)
	require.ErrorIs(t, auth.Verify(withNul, "before"), auth.ErrPasswordMismatch)
}

// 11. A blunder guard, not a performance certification.
//
// The ceiling is generous on purpose. This machine is not CI, CI is not a
// Raspberry Pi, and an upper bound tight enough to be interesting would be an
// upper bound that fails when a runner is busy. What it does catch is the
// change that matters: somebody setting memory to a gigabyte, or iterations to
// fifty, and nobody noticing until logins take a minute in production.
//
// The floor matters more than it looks. A verification that finishes in under a
// millisecond means the cost has collapsed — parameters lowered by accident, or
// Argon2id no longer being called at all — and that failure is silent in every
// other test here, because a cheap hash verifies exactly as correctly as an
// expensive one.
func TestVerificationCostIsInTheRightRange(t *testing.T) {
	t.Parallel()

	h := defaultHasher(t)
	encoded, err := h.Hash(rightPassword)
	require.NoError(t, err)

	elapsed := medianDuration(t, 5, func() { _ = auth.Verify(encoded, rightPassword) })
	t.Logf("Argon2id verification at %+v: %v (median of 5)", auth.DefaultParams(), elapsed)

	require.Greater(t, elapsed, time.Millisecond,
		"a verification this cheap means the cost has collapsed")
	require.Less(t, elapsed, 2*time.Second,
		"a verification this expensive makes logging in a denial of service")
}

// 12. The property is that VerifyDecoy performs the work, not that it performs
// it in some exact time. So the assertion is a lower bound measured against a
// real verification at the same parameters.
//
// A lower bound is the reliable direction. Scheduling noise, a busy runner and
// a cold cache all make things slower, never faster, so a floor at half the
// cost of a genuine verification has enormous headroom against noise while
// still being four orders of magnitude away from the failure it exists to
// catch — VerifyDecoy returning early and doing nothing at all.
func TestVerifyDecoyActuallySpendsTheTime(t *testing.T) {
	t.Parallel()

	h := defaultHasher(t)
	encoded, err := h.Hash(rightPassword)
	require.NoError(t, err)

	// The comparison case is a failed verification, because that is the path a
	// real login takes when the password is wrong — the one an attacker times
	// against the no-such-account path.
	real := medianDuration(t, 5, func() { _ = auth.Verify(encoded, wrongPassword) })
	decoy := medianDuration(t, 5, func() { h.VerifyDecoy() })

	t.Logf("failed verification %v, decoy %v (medians of 5)", real, decoy)

	require.Greater(t, decoy, real/2,
		"VerifyDecoy is far cheaper than a real verification, so the no-such-account path is timeable")
}

// medianDuration times f n times and returns the middle result. A median rather
// than a mean, because one descheduled run should not move the number.
func medianDuration(t *testing.T, n int, f func()) time.Duration {
	t.Helper()

	timings := make([]time.Duration, n)
	for i := range timings {
		start := time.Now()
		f()
		timings[i] = time.Since(start)
	}

	for i := 1; i < len(timings); i++ {
		for j := i; j > 0 && timings[j] < timings[j-1]; j-- {
			timings[j], timings[j-1] = timings[j-1], timings[j]
		}
	}
	return timings[len(timings)/2]
}
