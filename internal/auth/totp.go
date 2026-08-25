// SPDX-License-Identifier: AGPL-3.0-only

package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1" //nolint:gosec // HMAC-SHA1, not a digest — see the note on SHA1 below
	"crypto/sha256"
	"crypto/sha512"
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"hash"
	"net/url"
	"strings"
	"time"
)

// TOTP (RFC 6238) is written here rather than taken from a dependency, and the
// distinction from Argon2id in the file next door is the whole justification.
// Argon2id is a primitive: getting it wrong is subtle, and the failure is
// invisible. TOTP is an arrangement of primitives — an HMAC over a counter,
// then a documented truncation — and the RFC publishes test vectors that say,
// from outside this codebase, whether the arrangement is right. A thing with an
// external oracle is a thing worth writing.
//
// The primitives themselves are still the standard library's.

// Algorithm is the hash underneath the HMAC.
//
// The zero value is deliberately invalid, so that an Authenticator built
// without naming one is refused rather than defaulted into silently.
type Algorithm uint8

// The hashes RFC 6238 defines TOTP over.
const (
	// SHA1 is HMAC-SHA1, and it is the default for a compatibility reason
	// rather than a cryptographic one: it is the only algorithm every
	// authenticator app implements, and several ignore the algorithm parameter
	// in a provisioning URI altogether and assume it. Offering SHA256 by
	// default would produce codes that a user's app computes differently,
	// which presents as "2FA is broken" with nothing to debug.
	//
	// SHA-1's weakness is collision resistance, and HOTP depends on neither
	// collision nor preimage resistance: the construction is a MAC over a
	// counter with a 160-bit key, truncated to six digits. Those six digits
	// are the binding limit on guessing, by many orders of magnitude, and no
	// published attack on SHA-1 touches HMAC-SHA1. NIST still permits
	// HMAC-SHA1 for exactly this reason.
	SHA1 Algorithm = iota + 1

	// SHA256 and SHA512 exist because RFC 6238's test vectors cover them, and
	// those vectors are the only external proof this implementation is
	// correct. Supporting the algorithms is what makes the proof reachable.
	SHA256
	SHA512
)

// String names the algorithm the way a provisioning URI spells it.
func (a Algorithm) String() string {
	switch a {
	case SHA1:
		return "SHA1"
	case SHA256:
		return "SHA256"
	case SHA512:
		return "SHA512"
	default:
		return "unknown"
	}
}

func (a Algorithm) new() func() hash.Hash {
	switch a {
	case SHA1:
		return sha1.New //nolint:gosec // keyed MAC over a counter, not a digest
	case SHA256:
		return sha256.New
	case SHA512:
		return sha512.New
	default:
		return nil
	}
}

// Authenticator computes and checks time-based one-time passwords.
//
// The generating parameters and the checking parameters are the same struct on
// purpose. If the QR code a user scans said six digits over thirty seconds
// while the server checked eight over sixty, every code would be wrong and
// nothing would say why — so ProvisioningURI and Validate are methods on one
// value and cannot disagree.
type Authenticator struct {
	// Algorithm is the hash under the HMAC.
	Algorithm Algorithm

	// Digits is the length of a code, between 6 and 8.
	Digits int

	// Period is how long one code lasts. RFC 6238 calls it X and every
	// authenticator app assumes 30 seconds.
	Period time.Duration

	// Skew is how many periods either side of now are accepted. See
	// DefaultAuthenticator for why it is 1.
	Skew int
}

// DefaultAuthenticator returns the parameters Nusa uses for two-factor
// authentication: HMAC-SHA1, six digits, thirty seconds, one period of skew
// either side.
//
// # Why the skew is exactly one period, in each direction
//
// Behind, because of how people actually type. Someone glances at a code that
// has four seconds left on it, looks back at the keyboard and enters six
// digits; by the time it arrives the window has turned over. Refusing that
// login is not a security decision — the code was genuine and freshly read —
// and it produces exactly the behaviour that looks like an attack: the person
// immediately tries again, and the login rate limiter starts counting.
//
// Ahead, because the server's clock is the less trustworthy of the two. Phones
// discipline their time aggressively over the network. A self-hosted server
// under someone's television does not necessarily, and a server running a few
// seconds slow would otherwise reject every code from a correctly-set phone
// while appearing to work perfectly the rest of the time.
//
// Not more than one, because tolerance is bought with guessing resistance and
// the price is linear. Six digits is a million codes; accepting three windows
// makes three of them valid at any instant. Accepting eleven windows — a skew
// of five, which sounds modest — makes eleven valid, and multiplies the
// success rate of blind guessing by 3.7 against whatever the rate limiter
// allows. A clock more than 45 seconds out is a broken host, and the fix for a
// broken clock is NTP rather than a wider door.
//
// The consequence, stated plainly because it is what the replay counter has to
// deal with: at these settings a single code is acceptable for between 60 and
// 90 seconds depending on where in its period it was generated. That is a
// window in which the same code could be presented twice, which is why
// Validate returns the counter it matched and why the caller must record it.
func DefaultAuthenticator() Authenticator {
	return Authenticator{
		Algorithm: SHA1,
		Digits:    6,
		Period:    30 * time.Second,
		Skew:      1,
	}
}

// Validate checks the parameters themselves. It is called by every method that
// uses them, so an Authenticator assembled by hand cannot quietly produce
// codes nothing will ever match.
func (a Authenticator) validate() error {
	switch {
	case a.Algorithm.new() == nil:
		return fmt.Errorf("%w: unknown algorithm %d", ErrInvalidAuthenticator, a.Algorithm)
	case a.Digits < 6 || a.Digits > 8:
		// RFC 4226 sets the floor at 6. The ceiling is 8 because the dynamic
		// truncation yields a 31-bit number, and 10 digits would not be
		// uniformly distributed over it.
		return fmt.Errorf("%w: %d digits, must be between 6 and 8", ErrInvalidAuthenticator, a.Digits)
	case a.Period < time.Second:
		return fmt.Errorf("%w: period %v is below one second", ErrInvalidAuthenticator, a.Period)
	case a.Period%time.Second != 0:
		// The counter is a whole number of periods since the epoch, in
		// seconds. A fractional period has no meaning there.
		return fmt.Errorf("%w: period %v is not a whole number of seconds", ErrInvalidAuthenticator, a.Period)
	case a.Skew < 0:
		return fmt.Errorf("%w: skew %d is negative", ErrInvalidAuthenticator, a.Skew)
	case a.Skew > 10:
		return fmt.Errorf("%w: skew %d is wider than any clock error worth tolerating", ErrInvalidAuthenticator, a.Skew)
	}
	return nil
}

// minSecretLength is 16 bytes, RFC 4226's floor for a shared secret. Twenty is
// its recommendation and what NewSecret draws.
const minSecretLength = 16

// secretLength is what NewSecret draws: 160 bits, RFC 4226's recommendation and
// exactly HMAC-SHA1's block-independent key size. It also encodes to 32 base32
// characters with no padding, which is what an authenticator app expects to be
// handed when a QR code cannot be scanned.
const secretLength = 20

// NewSecret draws a fresh shared secret.
func NewSecret() ([]byte, error) {
	secret := make([]byte, secretLength)
	if _, err := rand.Read(secret); err != nil {
		return nil, fmt.Errorf("draw totp secret: %w", err)
	}
	return secret, nil
}

// base32NoPad is RFC 4648 base32 without padding, which is how every
// authenticator app writes a secret.
var base32NoPad = base32.StdEncoding.WithPadding(base32.NoPadding)

// EncodeSecret renders a secret the way an authenticator app displays it:
// uppercase base32, no padding.
func EncodeSecret(secret []byte) string {
	return base32NoPad.EncodeToString(secret)
}

// DecodeSecret reads a base32 secret back.
//
// It tolerates what a person copying a code by hand produces — lowercase, the
// spaces apps insert every four characters, hyphens, and trailing padding —
// because the alternative is refusing a secret that is correct and telling the
// user nothing useful about why. This is input normalisation of a machine
// value, not of user prose; nothing in the ledger's refusal to tidy what
// someone typed applies to a base32 blob that has exactly one valid reading.
func DecodeSecret(encoded string) ([]byte, error) {
	cleaned := strings.Map(func(r rune) rune {
		switch r {
		case ' ', '-', '\t', '\n', '\r', '=':
			return -1
		}
		return r
	}, encoded)
	cleaned = strings.ToUpper(cleaned)

	secret, err := base32NoPad.DecodeString(cleaned)
	if err != nil {
		return nil, fmt.Errorf("%w: not valid base32: %w", ErrInvalidSecret, err)
	}
	if len(secret) < minSecretLength {
		return nil, fmt.Errorf("%w: %d bytes, need at least %d", ErrInvalidSecret, len(secret), minSecretLength)
	}
	return secret, nil
}

// Code returns the code for secret at the given instant.
func (a Authenticator) Code(secret []byte, at time.Time) (string, error) {
	// Validated before the counter is derived, not after. counterAt divides by
	// the period, so an Authenticator carrying a zero period would panic here
	// rather than return the error CodeAt would have produced a line later —
	// and §12 rules out panicking outside main. Found by the parameter table
	// in TestUnusableAuthenticatorParametersAreRefused, which is the reason
	// that test calls every entry point rather than one representative one.
	if err := a.validate(); err != nil {
		return "", err
	}

	counter, err := a.counterAt(at)
	if err != nil {
		return "", err
	}
	return a.CodeAt(secret, counter)
}

// CodeAt returns the HOTP code for a counter directly (RFC 4226). TOTP is HOTP
// with the counter derived from the clock, and separating the two is what lets
// RFC 6238's test vectors be checked at the exact counters it publishes rather
// than at whatever a clock happens to say.
func (a Authenticator) CodeAt(secret []byte, counter uint64) (string, error) {
	if err := a.validate(); err != nil {
		return "", err
	}
	if len(secret) < minSecretLength {
		return "", fmt.Errorf("%w: %d bytes, need at least %d", ErrInvalidSecret, len(secret), minSecretLength)
	}

	var message [8]byte
	binary.BigEndian.PutUint64(message[:], counter)

	mac := hmac.New(a.Algorithm.new(), secret)
	mac.Write(message[:])
	sum := mac.Sum(nil)

	// Dynamic truncation, RFC 4226 §5.3. The low nibble of the last byte picks
	// where to read four bytes from, and the top bit of those is masked off so
	// the result does not depend on how the platform reads a sign bit.
	offset := sum[len(sum)-1] & 0x0f
	truncated := binary.BigEndian.Uint32(sum[offset:offset+4]) & 0x7fffffff

	return fmt.Sprintf("%0*d", a.Digits, truncated%pow10(a.Digits)), nil
}

// pow10 returns 10^n for the digit counts validate permits. A loop rather than
// math.Pow, because math.Pow returns an IEEE 754 double and this value decides
// what a code is.
func pow10(n int) uint32 {
	result := uint32(1)
	for range n {
		result *= 10
	}
	return result
}

// Validate checks a code presented at a given instant and returns the counter
// it matched.
//
// The returned counter is not informational. A code stays acceptable across
// every window Skew admits — between 60 and 90 seconds at the default settings
// — so nothing here can stop the same code being presented twice within that
// span. The caller closes that door by storing the returned counter and
// passing it back as lastUsed on the next attempt; this function then refuses
// anything at or below it with ErrCodeReused.
//
// lastUsed is zero when no code has been accepted yet. Real counters are the
// number of periods since 1970 and are nowhere near zero, so there is no
// ambiguity between "none yet" and a genuine value.
//
// ErrCodeReused is distinguished from ErrInvalidCode because the two are
// different events to whoever reads the logs: one is a mistyped code, the other
// is a correct code arriving twice, which is what a replayed credential looks
// like. The person signing in must be told the same thing either way.
func (a Authenticator) Validate(secret []byte, code string, at time.Time, lastUsed uint64) (uint64, error) {
	if err := a.validate(); err != nil {
		return 0, err
	}

	now, err := a.counterAt(at)
	if err != nil {
		return 0, err
	}

	// validate() has already bounded Skew to [0, 10], so widening it cannot
	// wrap. gosec's rule is syntactic and cannot see that; the suppression sits
	// on the one line that does the conversion so it stays tied to its guard.
	skew := uint64(a.Skew) //nolint:gosec // validate() bounds Skew to [0, 10]

	// Saturating, so that a clock near the epoch cannot wrap the low end round
	// to the top of the range. Only reachable in tests, and cheaper than
	// reasoning about whether it is reachable in production.
	low := uint64(0)
	if now > skew {
		low = now - skew
	}
	high := now + skew

	var matched bool
	var matchedCounter uint64

	for counter := low; counter <= high; counter++ {
		candidate, err := a.CodeAt(secret, counter)
		if err != nil {
			return 0, err
		}
		// Constant time, and the loop is deliberately not short-circuited: a
		// return the moment a window matches would make the time taken reveal
		// which window it was, and therefore the offset between the two
		// clocks. That is a small leak, but skipping the remaining iterations
		// buys nothing — there are three of them.
		if subtle.ConstantTimeCompare([]byte(candidate), []byte(code)) == 1 {
			matched = true
			matchedCounter = counter
		}
	}

	if !matched {
		return 0, ErrInvalidCode
	}
	if matchedCounter <= lastUsed {
		return 0, fmt.Errorf("%w: counter %d was already spent", ErrCodeReused, matchedCounter)
	}
	return matchedCounter, nil
}

// counterAt converts an instant into the number of periods since the Unix
// epoch, which is RFC 6238's T with T0 = 0.
//
// A time before the epoch is refused rather than clamped. The counter is
// unsigned, so a negative second would wrap to an enormous value and produce a
// code that is wrong in a way nothing downstream could detect.
func (a Authenticator) counterAt(at time.Time) (uint64, error) {
	seconds := at.Unix()
	if seconds < 0 {
		return 0, fmt.Errorf("%w: %s is before the Unix epoch", ErrTimeBeforeEpoch, at.UTC().Format(time.RFC3339))
	}
	// Both conversions are guarded: seconds by the check immediately above,
	// and the period by validate(), which requires at least one whole second.
	period := uint64(a.Period / time.Second) //nolint:gosec // validate() requires Period >= 1s
	return uint64(seconds) / period, nil
}

// ProvisioningURI builds the otpauth:// URI an authenticator app reads from a
// QR code.
//
// issuer names the Nusa instance and account names the person, conventionally
// their email address. Both appear twice — once in the path label and once as a
// query parameter — because that is what the de-facto specification asks for
// and older apps read only one of the two.
//
// The algorithm, digits and period are written out even though they are this
// package's defaults. Apps that read them then agree with the server, and apps
// that ignore them assume exactly these values anyway.
func (a Authenticator) ProvisioningURI(secret []byte, issuer, account string) (string, error) {
	if err := a.validate(); err != nil {
		return "", err
	}
	if len(secret) < minSecretLength {
		return "", fmt.Errorf("%w: %d bytes, need at least %d", ErrInvalidSecret, len(secret), minSecretLength)
	}
	if issuer == "" || account == "" {
		return "", fmt.Errorf("%w: issuer and account are both required", ErrInvalidAuthenticator)
	}
	// A colon separates issuer from account in the label, so neither may
	// contain one. Escaping it would produce a label that renders as two
	// fields in some apps and one in others.
	if strings.ContainsRune(issuer, ':') || strings.ContainsRune(account, ':') {
		return "", fmt.Errorf("%w: issuer and account must not contain a colon", ErrInvalidAuthenticator)
	}

	query := url.Values{}
	query.Set("secret", EncodeSecret(secret))
	query.Set("issuer", issuer)
	query.Set("algorithm", a.Algorithm.String())
	query.Set("digits", fmt.Sprint(a.Digits))
	query.Set("period", fmt.Sprint(int(a.Period/time.Second)))

	uri := url.URL{
		Scheme:   "otpauth",
		Host:     "totp",
		Path:     "/" + issuer + ":" + account,
		RawQuery: query.Encode(),
	}
	return uri.String(), nil
}
