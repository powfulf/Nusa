// SPDX-License-Identifier: AGPL-3.0-only

package auth_test

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/powfulf/Nusa/internal/auth"
)

// What these guards must cover. Written before they were, and each item was
// broken on purpose and watched failing afterwards (CLAUDE.md §11).
//
//	 1. Every RFC 6238 Appendix B vector: 3 algorithms x 6 instants.
//	 2. The counter derived from a clock matches the T the RFC publishes.
//	 3. A code holds for its whole period and changes at the boundary.
//	 4. Skew accepts exactly one window either side, and no further.
//	 5. A spent counter is refused, and refused by a distinguishable name.
//	 6. Replay inside the skew window — the case skew itself opens.
//	 7. A wrong code is ErrInvalidCode.
//	 8. A time before the epoch is refused, not wrapped.
//	 9. Every unusable parameter shape is refused.
//	10. Secrets round-trip, and decoding tolerates how people transcribe them.
//	11. A code is always exactly Digits long, leading zeros included.
//	12. A provisioning URI carries the parameters validation will use.

// The RFC 6238 Appendix B seeds, and the trap in them.
//
// Appendix B's table has one "Secret" note reading 12345678901234567890, and
// taking that at face value for all three algorithms produces codes that do
// not match the table. The reference implementation in Appendix A is where the
// real answer is: it defines seed, seed32 and seed64, and HMAC-SHA256 and
// HMAC-SHA512 are run against the longer two. The ASCII digits simply repeat
// up to the required length.
//
// This is worth stating at length because of what the next person will do with
// it. Somebody comparing this implementation against the published table with
// a single 20-byte seed will get three correct answers and twelve wrong ones,
// conclude the SHA256 and SHA512 paths are broken, and "fix" code that was
// never wrong. The vectors below are the ones the RFC's own code produces.
const (
	seedSHA1   = "12345678901234567890"
	seedSHA256 = "12345678901234567890123456789012"
	seedSHA512 = "1234567890123456789012345678901234567890123456789012345678901234"
)

// rfcAuthenticator is Appendix B's configuration: T0 = 0, X = 30, eight digits.
// Eight, not the six Nusa ships — the vectors are published at eight, and
// checking them at six would be checking something else.
func rfcAuthenticator(alg auth.Algorithm) auth.Authenticator {
	return auth.Authenticator{Algorithm: alg, Digits: 8, Period: 30 * time.Second, Skew: 1}
}

type rfcVector struct {
	unixTime int64
	hexT     string // the "Value of T (Hex)" column
	sha1     string
	sha256   string
	sha512   string
}

// The whole of RFC 6238 Appendix B.
//
// The last row is worth being exact about, because it is easy to credit it with
// more than it does. 20000000000 seconds is the year 2603, far past what a
// 32-bit time_t holds, so the row does catch an implementation that narrows the
// clock reading on its way in — and that is the whole of what it catches.
//
// It says nothing about the counter. 20000000000 / 30 is 666666666, which is
// 0x27BC86AA and fits in 32 bits with room to spare, as does every other T in
// this table. So no published vector exercises a counter above 2^32, and
// narrowing the counter to uint32 passes all eighteen of them. That gap is
// closed by TestTheCounterIsCarriedAtFullWidth below, which is a
// self-consistency check rather than an external one — stated plainly, because
// a guard borrowing the RFC's authority for something the RFC never said is
// worse than no guard.
//
// Established the way the rule in §11 asks: by narrowing the counter to uint32
// and watching all eighteen vectors stay green.
var rfcVectors = []rfcVector{
	{59, "0000000000000001", "94287082", "46119246", "90693936"},
	{1111111109, "00000000023523EC", "07081804", "68084774", "25091201"},
	{1111111111, "00000000023523ED", "14050471", "67062674", "99943326"},
	{1234567890, "000000000273EF07", "89005924", "91819424", "93441116"},
	{2000000000, "0000000003F940AA", "69279037", "90698825", "38618901"},
	{20000000000, "0000000027BC86AA", "65353130", "77737706", "47863826"},
}

// 1, 2.
func TestRFC6238AppendixBVectors(t *testing.T) {
	t.Parallel()

	algorithms := []struct {
		name string
		alg  auth.Algorithm
		seed string
		want func(rfcVector) string
	}{
		{"SHA1", auth.SHA1, seedSHA1, func(v rfcVector) string { return v.sha1 }},
		{"SHA256", auth.SHA256, seedSHA256, func(v rfcVector) string { return v.sha256 }},
		{"SHA512", auth.SHA512, seedSHA512, func(v rfcVector) string { return v.sha512 }},
	}

	checked := 0
	for _, a := range algorithms {
		t.Run(a.name, func(t *testing.T) {
			t.Parallel()
			authr := rfcAuthenticator(a.alg)
			secret := []byte(a.seed)

			for _, v := range rfcVectors {
				t.Run(strconv.FormatInt(v.unixTime, 10), func(t *testing.T) {
					t.Parallel()
					want := a.want(v)

					// From the clock. This is the TOTP half: it proves the
					// counter is derived as floor((T - T0) / X) with T0 = 0.
					fromClock, err := authr.Code(secret, time.Unix(v.unixTime, 0).UTC())
					require.NoError(t, err)
					require.Equal(t, want, fromClock,
						"RFC 6238 Appendix B, %s at t=%d", a.name, v.unixTime)

					// From the counter the RFC publishes in its own hex column.
					// This is the HOTP half, and separating the two says which
					// one is wrong when one of them is.
					counter, err := strconv.ParseUint(v.hexT, 16, 64)
					require.NoError(t, err)
					fromCounter, err := authr.CodeAt(secret, counter)
					require.NoError(t, err)
					require.Equal(t, want, fromCounter,
						"RFC 6238 Appendix B, %s at T=%s", a.name, v.hexT)

					// The two halves must agree, which is the statement that
					// the derivation and the published T are the same number.
					require.Equal(t, fromClock, fromCounter)
				})
			}
		})
		checked += len(rfcVectors)
	}

	require.Equal(t, 18, checked, "all 18 published vectors must be exercised")
}

// The seed trap, asserted rather than only described. If a later change makes
// the SHA256 path use the 20-byte seed, this says so in one line instead of
// leaving somebody to rediscover it against the table.
func TestTheShorterSeedDoesNotProduceTheSHA256Vectors(t *testing.T) {
	t.Parallel()

	authr := rfcAuthenticator(auth.SHA256)
	code, err := authr.Code([]byte(seedSHA1), time.Unix(59, 0).UTC())
	require.NoError(t, err)
	require.NotEqual(t, "46119246", code,
		"Appendix B's SHA256 vector is against the 32-byte seed, not the 20-byte one")
}

// The 64-bit counter, which no published vector reaches.
//
// RFC 4226 §5.1 specifies the HMAC message as the counter in eight bytes, big
// endian. Every Appendix B counter fits in four of them, so the upper four are
// zero in all eighteen vectors and an implementation that dropped them would
// match the table perfectly.
//
// This is therefore not an external oracle and does not pretend to be. It is a
// differential check: two counters that are equal in their low 32 bits and
// differ in their high 32 bits must produce different codes. A narrowed
// counter makes each pair identical, which is the exact failure the vectors
// cannot see.
func TestTheCounterIsCarriedAtFullWidth(t *testing.T) {
	t.Parallel()

	a := rfcAuthenticator(auth.SHA1)
	secret := []byte(seedSHA1)

	pairs := []struct{ low, high uint64 }{
		{0, 1 << 32},
		{1, 1<<32 | 1},
		{0x27BC86AA, 1<<32 | 0x27BC86AA}, // the largest T the RFC publishes
		{0xFFFFFFFF, 1<<32 | 0xFFFFFFFF},
	}

	for _, p := range pairs {
		require.Equal(t, p.low, p.high&0xFFFFFFFF,
			"the pair must agree in its low 32 bits, or this proves nothing about the high ones")

		lowCode, err := a.CodeAt(secret, p.low)
		require.NoError(t, err)
		highCode, err := a.CodeAt(secret, p.high)
		require.NoError(t, err)

		require.NotEqual(t, lowCode, highCode,
			"counters %d and %d differ only above bit 32 and produced the same code, so the counter is being truncated",
			p.low, p.high)
	}
}

func nusaAuthenticator() auth.Authenticator { return auth.DefaultAuthenticator() }

func testSecret(t *testing.T) []byte {
	t.Helper()
	secret, err := auth.NewSecret()
	require.NoError(t, err)
	return secret
}

// 3. The boundary. A period is [n*30, n*30+30), so 29 and 30 must differ and
// 30 and 59 must not.
func TestACodeHoldsForItsPeriodAndChangesAtTheBoundary(t *testing.T) {
	t.Parallel()

	a := nusaAuthenticator()
	secret := testSecret(t)

	code := func(sec int64) string {
		t.Helper()
		c, err := a.Code(secret, time.Unix(sec, 0).UTC())
		require.NoError(t, err)
		return c
	}

	require.Equal(t, code(0), code(29), "0 and 29 are the same period")
	require.NotEqual(t, code(29), code(30), "30 opens a new period")
	require.Equal(t, code(30), code(59), "30 and 59 are the same period")
	require.NotEqual(t, code(59), code(60), "60 opens a new period")

	// The first and last second of one period, named as such, because the
	// off-by-one this catches is the one where a period is treated as
	// (n*30, n*30+30].
	require.Equal(t, code(300), code(329))
	require.NotEqual(t, code(329), code(330))
}

// 4. Skew, in both directions, and its edge.
func TestSkewAcceptsExactlyOneWindowEitherSide(t *testing.T) {
	t.Parallel()

	a := nusaAuthenticator()
	secret := testSecret(t)

	// A code generated at this instant, checked at instants around it.
	const generatedAt = 1_700_000_000
	code, err := a.Code(secret, time.Unix(generatedAt, 0).UTC())
	require.NoError(t, err)

	accepted := func(offsetSeconds int64) bool {
		_, err := a.Validate(secret, code, time.Unix(generatedAt+offsetSeconds, 0).UTC(), 0)
		return err == nil
	}

	require.True(t, accepted(0), "the current window")
	require.True(t, accepted(30), "one window later — the server clock is behind the phone")
	require.True(t, accepted(-30), "one window earlier — the code was read late and typed slowly")

	require.False(t, accepted(60), "two windows later is beyond the stated tolerance")
	require.False(t, accepted(-60), "two windows earlier is beyond the stated tolerance")

	// Skew zero must mean zero, or the constant in DefaultAuthenticator is
	// decorative and the real window is whatever the loop happens to do.
	strict := a
	strict.Skew = 0
	_, err = strict.Validate(secret, code, time.Unix(generatedAt, 0).UTC(), 0)
	require.NoError(t, err)
	_, err = strict.Validate(secret, code, time.Unix(generatedAt+30, 0).UTC(), 0)
	require.ErrorIs(t, err, auth.ErrInvalidCode, "skew 0 must accept nothing but the current window")
}

// 5, 6. Replay. The second of these is the case skew itself creates, and it is
// the reason Validate returns a counter at all.
func TestASpentCodeIsRefused(t *testing.T) {
	t.Parallel()

	a := nusaAuthenticator()
	secret := testSecret(t)
	at := time.Unix(1_700_000_000, 0).UTC()

	code, err := a.Code(secret, at)
	require.NoError(t, err)

	counter, err := a.Validate(secret, code, at, 0)
	require.NoError(t, err)
	require.NotZero(t, counter, "a real counter is periods since 1970, never zero")

	t.Run("the same code again at the same instant", func(t *testing.T) {
		_, err := a.Validate(secret, code, at, counter)
		require.ErrorIs(t, err, auth.ErrCodeReused)
		require.NotErrorIs(t, err, auth.ErrInvalidCode,
			"a replayed valid code is a different event from a mistyped one")
	})

	t.Run("the same code one window later, still inside skew", func(t *testing.T) {
		// Without the counter this would be indistinguishable from a fresh
		// login: the code is still acceptable at this instant, because skew
		// admits the window it came from.
		later := at.Add(30 * time.Second)
		fresh, err := a.Validate(secret, code, later, 0)
		require.NoError(t, err, "skew does still admit it, which is the problem")
		require.Equal(t, counter, fresh, "and it is the same counter")

		_, err = a.Validate(secret, code, later, counter)
		require.ErrorIs(t, err, auth.ErrCodeReused)
	})

	t.Run("the next window's code is not a replay", func(t *testing.T) {
		later := at.Add(30 * time.Second)
		next, err := a.Code(secret, later)
		require.NoError(t, err)

		nextCounter, err := a.Validate(secret, next, later, counter)
		require.NoError(t, err, "a genuinely new code must still be accepted")
		require.Equal(t, counter+1, nextCounter)
	})
}

// 7, 8.
func TestAWrongCodeAndAnImpossibleTimeAreRefused(t *testing.T) {
	t.Parallel()

	a := nusaAuthenticator()
	secret := testSecret(t)
	at := time.Unix(1_700_000_000, 0).UTC()

	right, err := a.Code(secret, at)
	require.NoError(t, err)

	for name, code := range map[string]string{
		"empty":       "",
		"too short":   right[:5],
		"too long":    right + "0",
		"not digits":  "abcdef",
		"all zeros":   "000000",
		"transposed":  right[1:2] + right[0:1] + right[2:],
		"a digit out": nudge(right),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if code == right {
				t.Skip("this mutation happened to produce the right code")
			}
			_, err := a.Validate(secret, code, at, 0)
			require.ErrorIs(t, err, auth.ErrInvalidCode)
		})
	}

	t.Run("before the epoch", func(t *testing.T) {
		t.Parallel()
		_, err := a.Code(secret, time.Unix(-1, 0).UTC())
		require.ErrorIs(t, err, auth.ErrTimeBeforeEpoch)

		_, err = a.Validate(secret, right, time.Unix(-1, 0).UTC(), 0)
		require.ErrorIs(t, err, auth.ErrTimeBeforeEpoch,
			"a negative second must not wrap into an enormous counter")
	})
}

// nudge changes the last digit, so the result is one digit away from correct.
func nudge(code string) string {
	last := code[len(code)-1]
	if last == '9' {
		return code[:len(code)-1] + "0"
	}
	return code[:len(code)-1] + string(last+1)
}

// 9.
func TestUnusableAuthenticatorParametersAreRefused(t *testing.T) {
	t.Parallel()

	ok := auth.DefaultAuthenticator()
	secret := testSecret(t)

	bad := map[string]auth.Authenticator{
		"no algorithm":      {Digits: 6, Period: 30 * time.Second, Skew: 1},
		"unknown algorithm": {Algorithm: auth.Algorithm(99), Digits: 6, Period: 30 * time.Second, Skew: 1},
		"five digits":       {Algorithm: auth.SHA1, Digits: 5, Period: 30 * time.Second, Skew: 1},
		"nine digits":       {Algorithm: auth.SHA1, Digits: 9, Period: 30 * time.Second, Skew: 1},
		"zero period":       {Algorithm: auth.SHA1, Digits: 6, Period: 0, Skew: 1},
		"fractional period": {Algorithm: auth.SHA1, Digits: 6, Period: 1500 * time.Millisecond, Skew: 1},
		"negative skew":     {Algorithm: auth.SHA1, Digits: 6, Period: 30 * time.Second, Skew: -1},
		"absurd skew":       {Algorithm: auth.SHA1, Digits: 6, Period: 30 * time.Second, Skew: 100},
	}

	at := time.Unix(1_700_000_000, 0).UTC()
	for name, a := range bad {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			// Every entry point checks, not only one of them. A generator that
			// validated and a validator that did not would let a misconfigured
			// server issue codes it then rejects.
			_, err := a.Code(secret, at)
			require.ErrorIs(t, err, auth.ErrInvalidAuthenticator, "Code")

			_, err = a.CodeAt(secret, 1)
			require.ErrorIs(t, err, auth.ErrInvalidAuthenticator, "CodeAt")

			_, err = a.Validate(secret, "123456", at, 0)
			require.ErrorIs(t, err, auth.ErrInvalidAuthenticator, "Validate")

			_, err = a.ProvisioningURI(secret, "Nusa", "budi@example.org")
			require.ErrorIs(t, err, auth.ErrInvalidAuthenticator, "ProvisioningURI")
		})
	}

	_, err := ok.Code(secret, at)
	require.NoError(t, err, "DefaultAuthenticator must itself be acceptable")

	// A secret below RFC 4226's floor is refused everywhere too.
	short := []byte("too short")
	_, err = ok.Code(short, at)
	require.ErrorIs(t, err, auth.ErrInvalidSecret)
	_, err = ok.ProvisioningURI(short, "Nusa", "budi@example.org")
	require.ErrorIs(t, err, auth.ErrInvalidSecret)
}

// 10.
func TestSecretsRoundTripAndToleratedTranscription(t *testing.T) {
	t.Parallel()

	secret, err := auth.NewSecret()
	require.NoError(t, err)
	require.Len(t, secret, 20, "RFC 4226 recommends 160 bits")

	encoded := auth.EncodeSecret(secret)
	require.Len(t, encoded, 32, "20 bytes is 32 unpadded base32 characters")
	require.NotContains(t, encoded, "=")
	require.Equal(t, strings.ToUpper(encoded), encoded)

	back, err := auth.DecodeSecret(encoded)
	require.NoError(t, err)
	require.Equal(t, secret, back)

	// Two secrets in a row must differ, or NewSecret is not drawing randomness.
	other, err := auth.NewSecret()
	require.NoError(t, err)
	require.NotEqual(t, secret, other)

	// How a person actually transcribes one: apps show it in groups of four,
	// and a keyboard produces lowercase.
	for name, written := range map[string]string{
		"as displayed":  encoded,
		"lowercase":     strings.ToLower(encoded),
		"spaced fours":  groupsOfFour(encoded, " "),
		"hyphenated":    groupsOfFour(encoded, "-"),
		"padded":        encoded + "======",
		"with newlines": encoded[:16] + "\n" + encoded[16:],
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got, err := auth.DecodeSecret(written)
			require.NoError(t, err)
			require.Equal(t, secret, got)
		})
	}

	for name, written := range map[string]string{
		"empty":        "",
		"not base32":   "0189!!!!",
		"digit one":    strings.Repeat("1", 32), // 1 is not in the base32 alphabet
		"too short":    auth.EncodeSecret([]byte("fifteen bytes!!")),
		"exactly zero": auth.EncodeSecret(nil),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := auth.DecodeSecret(written)
			require.ErrorIs(t, err, auth.ErrInvalidSecret)
		})
	}
}

func groupsOfFour(s, sep string) string {
	var b strings.Builder
	for i := 0; i < len(s); i += 4 {
		if i > 0 {
			b.WriteString(sep)
		}
		b.WriteString(s[i:min(i+4, len(s))])
	}
	return b.String()
}

// 11. A code is a fixed-width string, and a value below 10^(digits-1) must be
// padded rather than shortened.
//
// The assertion that matters is the last one. Scanning counters until a padded
// code turns up and then asserting nothing about it would be a test that passes
// whether or not padding works, so the count of padded codes found is itself
// checked: if the scan never produced one, the test says so instead of
// reporting success on an empty sample.
func TestACodeIsAlwaysExactlyDigitsLong(t *testing.T) {
	t.Parallel()

	secret := testSecret(t)

	for _, digits := range []int{6, 7, 8} {
		t.Run(strconv.Itoa(digits), func(t *testing.T) {
			t.Parallel()
			a := auth.Authenticator{Algorithm: auth.SHA1, Digits: digits, Period: 30 * time.Second, Skew: 1}

			padded := 0
			const scanned = 2000
			for counter := uint64(1); counter <= scanned; counter++ {
				code, err := a.CodeAt(secret, counter)
				require.NoError(t, err)
				require.Len(t, code, digits, "counter %d produced %q", counter, code)
				require.Regexp(t, `^[0-9]+$`, code)
				if code[0] == '0' {
					padded++
				}
			}

			// Roughly a tenth of codes start with a zero, so over 2000 the
			// chance of finding none is nil. If it ever is none, the loop above
			// proved nothing about padding and must be told so.
			require.NotZero(t, padded,
				"scanned %d counters and found no code needing a leading zero, so padding went untested", scanned)
			t.Logf("%d of %d codes at %d digits needed a leading zero", padded, scanned, digits)
		})
	}
}

// 12.
func TestAProvisioningURICarriesTheParametersValidationUses(t *testing.T) {
	t.Parallel()

	a := nusaAuthenticator()
	secret := testSecret(t)

	uri, err := a.ProvisioningURI(secret, "Nusa", "budi@example.org")
	require.NoError(t, err)

	require.True(t, strings.HasPrefix(uri, "otpauth://totp/"), "got %q", uri)
	require.Contains(t, uri, "secret="+auth.EncodeSecret(secret))
	require.Contains(t, uri, "algorithm=SHA1")
	require.Contains(t, uri, "digits=6")
	require.Contains(t, uri, "period=30")
	require.Contains(t, uri, "issuer=Nusa")

	// The label carries issuer and account separated by a colon. '@' stays
	// literal, which is both legal in a path segment and what the otpauth
	// convention's own examples show; a space does not, and must arrive
	// percent-encoded or an app truncates the label at it.
	require.Contains(t, uri, "/Nusa:budi@example.org?")

	spaced, err := a.ProvisioningURI(secret, "Nusa Home", "budi@example.org")
	require.NoError(t, err)
	require.Contains(t, spaced, "/Nusa%20Home:budi@example.org?")
	require.Contains(t, spaced, "issuer=Nusa+Home")

	// The parameters in the URI are the ones Validate will use. This is the
	// point of ProvisioningURI being a method: a URI saying eight digits while
	// the server checks six produces codes that are always wrong.
	eight := a
	eight.Digits = 8
	eight.Algorithm = auth.SHA512
	eight.Period = 60 * time.Second
	other, err := eight.ProvisioningURI(secret, "Nusa", "budi@example.org")
	require.NoError(t, err)
	require.Contains(t, other, "digits=8")
	require.Contains(t, other, "algorithm=SHA512")
	require.Contains(t, other, "period=60")

	// A colon in either field would split the label in a way apps disagree
	// about, so it is refused rather than escaped.
	_, err = a.ProvisioningURI(secret, "Nusa: Personal Finance", "budi@example.org")
	require.ErrorIs(t, err, auth.ErrInvalidAuthenticator)
	_, err = a.ProvisioningURI(secret, "Nusa", "budi:budi@example.org")
	require.ErrorIs(t, err, auth.ErrInvalidAuthenticator)

	for name, args := range map[string][2]string{
		"no issuer":  {"", "budi@example.org"},
		"no account": {"Nusa", ""},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := a.ProvisioningURI(secret, args[0], args[1])
			require.ErrorIs(t, err, auth.ErrInvalidAuthenticator)
		})
	}
}
