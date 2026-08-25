// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"net/netip"
	"time"

	"github.com/GaffaQ/Nusa/internal/auth"
)

// PasswordHasher is what this layer needs from auth.Hasher.
//
// It is an interface rather than the concrete type for one reason, and it is
// not testability in general: VerifyDecoy has no return value and no visible
// effect, so the only way to prove the no-such-account path actually calls it
// is to substitute something that counts. A timing assertion would be the
// alternative, and a timing assertion in CI is a flake waiting to happen.
type PasswordHasher interface {
	Hash(password string) (string, error)
	NeedsRehash(encoded string) (bool, error)

	// VerifyDecoy spends the cost of a verification against a hash nobody
	// knows the input to. The no-such-account path must call it, or the time a
	// response takes says whether an address is registered.
	VerifyDecoy()
}

// AuthDeps is everything the authentication endpoints need.
//
// Nothing here is optional. A zero field is a misconfiguration that would
// surface as a nil dereference on somebody's first sign-in, so NewRouter
// refuses to mount the routes unless the whole set is present.
type AuthDeps struct {
	Credentials   auth.CredentialStore
	Sessions      auth.SessionStore
	SecondFactors auth.SecondFactorStore
	Events        auth.EventRecorder

	Hasher        PasswordHasher
	Authenticator auth.Authenticator
	Limiter       *auth.Limiter

	// TrustedProxies is passed to ClientIP. Empty means no header is believed,
	// which is the correct default and not a missing value.
	TrustedProxies []netip.Prefix

	SessionTTL time.Duration

	// SecureCookies comes from Config.SecureCookies and is not separately
	// configurable. See sessioncookie.go.
	SecureCookies bool

	// Now and NewID are injected so that tests are deterministic. In
	// production they are time.Now and NewUUIDv7.
	Now   func() time.Time
	NewID func() (string, error)
}

func (d *AuthDeps) ready() bool {
	return d != nil &&
		d.Credentials != nil && d.Sessions != nil && d.SecondFactors != nil &&
		d.Events != nil && d.Hasher != nil && d.Limiter != nil &&
		d.SessionTTL > 0 && d.Now != nil && d.NewID != nil
}

func (d *AuthDeps) now() time.Time { return d.Now().UTC() }

// NewUUIDv7 mints an identity of the shape §5.7 requires: a canonical
// lowercase UUIDv7.
//
// Written here rather than taken from a dependency because it is fifteen lines
// of bit-shuffling with a specification, and because identities are minted at
// the edge by design — the domain never generates one and never reads a clock.
// This is the edge.
//
// Layout, per RFC 9562 §5.7: 48 bits of Unix milliseconds, four bits of
// version, twelve bits of randomness, two bits of variant, sixty-two more bits
// of randomness. The timestamp prefix is what makes these sort by creation
// time, which is why the schema wanted v7 rather than v4 in the first place.
func NewUUIDv7() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[6:]); err != nil {
		return "", fmt.Errorf("draw uuid randomness: %w", err)
	}

	ms := uint64(time.Now().UTC().UnixMilli())
	var stamp [8]byte
	binary.BigEndian.PutUint64(stamp[:], ms)
	copy(b[0:6], stamp[2:8]) // the low 48 bits

	b[6] = (b[6] & 0x0f) | 0x70 // version 7
	b[8] = (b[8] & 0x3f) | 0x80 // RFC 4122 variant

	const hexDigits = "0123456789abcdef"
	out := make([]byte, 0, 36)
	for i, v := range b {
		if i == 4 || i == 6 || i == 8 || i == 10 {
			out = append(out, '-')
		}
		out = append(out, hexDigits[v>>4], hexDigits[v&0x0f])
	}
	return string(out), nil
}
