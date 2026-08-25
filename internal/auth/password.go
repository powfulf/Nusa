// SPDX-License-Identifier: AGPL-3.0-only

package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// Argon2id is used through golang.org/x/crypto rather than written here. That
// is a deliberate exception to Nusa's preference for the standard library: a
// memory-hard key derivation function is a cryptographic primitive, and a
// hand-rolled one is a liability no amount of testing repairs. TOTP is written
// by hand in this same package because it is an arrangement of primitives with
// published test vectors; Argon2id is a primitive.
//
// What is written here is everything around it: the parameters, the encoding,
// the constant-time comparison, and the decoy path that keeps a missing
// account from being distinguishable with a stopwatch.

// Params is the cost of one Argon2id hash.
type Params struct {
	// Memory is the size of the block array, in KiB.
	Memory uint32

	// Iterations is the number of passes made over that array.
	Iterations uint32

	// Parallelism is the number of lanes the array is split into.
	Parallelism uint8

	// SaltLength is how many random bytes are drawn for each hash.
	SaltLength uint32

	// TagLength is the length of the derived key, in bytes.
	TagLength uint32
}

// DefaultParams returns the cost Nusa hashes passwords at.
//
// Every number below is chosen for this application's deployment rather than
// copied from an example. The governing fact is that Nusa is self-hosted: the
// machine is a Raspberry Pi, a 1 vCPU / 1 GiB VPS or a NAS, not a login farm.
// The budget is therefore set by what the weakest plausible host survives while
// a household signs in at once, not by what a developer's laptop can do.
//
// Memory = 19456 KiB (19 MiB). Memory is the dimension that actually costs an
// attacker something. Iterations parallelise across cores almost perfectly;
// memory does not, and a GPU has thousands of cores against a few gigabytes of
// RAM. At 19 MiB per candidate an 8 GiB card holds roughly 430 of them at once
// however many cores it has, and that number — not the core count — is what
// bounds a cracking run. The ceiling comes from the other side: concurrency
// multiplies it. Sixteen simultaneous verifications at 19 MiB is about 304 MiB,
// which a 1 GiB host absorbs. The next commonly cited figure, 64 MiB, is 1 GiB
// at that same concurrency and would push such a host into swap, where a
// verification takes seconds and the machine stops serving anything at all. A
// password hash that a burst of logins can turn into an outage has traded one
// security property for another.
//
// Iterations = 2, derived from the target rather than adopted. With memory
// fixed, iterations are the only remaining lever on cost, and the figure to aim
// for is the familiar one for an interactive login: a couple of hundred
// milliseconds on the server, which is imperceptible to someone who has just
// finished typing and ruinous to someone trying billions of guesses.
//
// Measured here, two passes at 19 MiB costs 28 ms on a current desktop
// (35 ms under -race). A Raspberry Pi 4 — the slowest host Nusa is meant to
// run on — is roughly five to eight times slower at memory-bound work, which
// puts the same hash at 150–220 ms there. That is the target, on the machine
// the target was written for, which is why the number is 2 and not 3.
//
// It follows that raising this is the lever to reach for if the floor hardware
// ever moves, because iterations cost time without costing memory and so do
// not disturb the concurrency arithmetic above.
//
// Parallelism = 1. Lanes split the same array rather than adding to it, so
// parallelism buys wall-clock time only where there are spare cores, and on the
// single-vCPU host that is the floor here there are none. It would also make
// the cost of a hash depend on the machine verifying it, so a credential minted
// on a large server would verify slowly on a small one. One lane makes the cost
// the same everywhere, which is the whole point of storing the parameters.
//
// SaltLength = 16 bytes. RFC 9106's recommendation. 128 bits of salt is past
// the point where a precomputed table for even one popular password is
// worth building.
//
// TagLength = 32 bytes. 256 bits, matching the rest of Nusa's secrets. The tag
// is never the weak link and there is no reason for it to be shorter.
func DefaultParams() Params {
	return Params{
		Memory:      19456,
		Iterations:  2,
		Parallelism: 1,
		SaltLength:  16,
		TagLength:   32,
	}
}

// MaxVerifiableMemory bounds the memory an encoded hash may ask for when it is
// read back, at 1 GiB.
//
// It is exported because it is a contract rather than an internal detail:
// anything that decides what cost to mint at has to stay at or below it, or it
// will produce hashes this package then refuses to verify. internal/config
// validates the operator-supplied cost against this constant rather than
// against a copy of the number.
//
// Encoded hashes come from Nusa's own database, so this is not defending
// against a hostile user today. It defends against a planted or corrupted row,
// and against the day credentials are imported from somewhere else: without a
// ceiling, one row reading m=17179869184 turns a single login attempt into an
// out-of-memory kill, and the number deciding how much to allocate arrives
// from storage rather than from configuration.
const MaxVerifiableMemory = 1 << 20

// maxSaltOrTagLength bounds how long a stored salt or tag may be, at 64 bytes.
// Argon2id's own recommendations are 16 and 32; four times that leaves room for
// a future change without leaving room for a nonsense one.
const maxSaltOrTagLength = 64

// argon2Version is the only Argon2 version this package produces or accepts:
// 0x13, the version RFC 9106 specifies. A hash carrying anything else was not
// written by us and is refused rather than guessed at.
const argon2Version = argon2.Version

// Hasher mints password hashes at a fixed cost.
//
// Verifying needs no Hasher, because an encoded hash carries the parameters it
// was made with — that is the entire reason for the PHC string format. A Hasher
// represents current policy instead: what new passwords cost, what existing
// hashes are compared against when deciding whether to rehash, and how long a
// login must take when there is no account to check.
type Hasher struct {
	params Params

	// decoy is a syntactically valid encoded hash carrying params' costs and a
	// tag nothing will ever match. See VerifyDecoy.
	decoy string
}

// NewHasher returns a Hasher that mints hashes at the given cost.
func NewHasher(p Params) (Hasher, error) {
	switch {
	case p.Memory < 8:
		// Argon2 needs at least 8 KiB per lane; below that it will not run.
		return Hasher{}, fmt.Errorf("%w: memory %d KiB is below the 8 KiB floor", ErrInvalidParameters, p.Memory)
	case p.Memory > MaxVerifiableMemory:
		return Hasher{}, fmt.Errorf("%w: memory %d KiB exceeds the %d KiB ceiling", ErrInvalidParameters, p.Memory, MaxVerifiableMemory)
	case p.Iterations < 1:
		return Hasher{}, fmt.Errorf("%w: iterations must be at least 1", ErrInvalidParameters)
	case p.Parallelism < 1:
		return Hasher{}, fmt.Errorf("%w: parallelism must be at least 1", ErrInvalidParameters)
	case uint32(p.Parallelism)*8 > p.Memory:
		// The primitive panics on this rather than returning an error, so it
		// is refused here where the caller gets something it can handle.
		return Hasher{}, fmt.Errorf("%w: %d lanes need at least %d KiB of memory, have %d",
			ErrInvalidParameters, p.Parallelism, uint32(p.Parallelism)*8, p.Memory)
	case p.SaltLength < 8:
		return Hasher{}, fmt.Errorf("%w: salt length %d is below the 8 byte floor", ErrInvalidParameters, p.SaltLength)
	case p.TagLength < 16:
		return Hasher{}, fmt.Errorf("%w: tag length %d is below the 16 byte floor", ErrInvalidParameters, p.TagLength)
	}

	decoy, err := newDecoy(p)
	if err != nil {
		return Hasher{}, err
	}
	return Hasher{params: p, decoy: decoy}, nil
}

// Params returns the cost this Hasher mints at.
func (h Hasher) Params() Params { return h.params }

// Hash derives an encoded Argon2id hash of password with a fresh random salt.
//
// The result is a PHC string:
//
//	$argon2id$v=19$m=19456,t=2,p=1$<base64 salt>$<base64 tag>
//
// The parameters travel with the hash on purpose. Raising the cost later must
// not invalidate every existing credential, and it does not: an old hash still
// says how to verify itself, and NeedsRehash says that it is old.
func (h Hasher) Hash(password string) (string, error) {
	salt := make([]byte, h.params.SaltLength)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("draw password salt: %w", err)
	}

	tag := argon2.IDKey(
		[]byte(password), salt,
		h.params.Iterations, h.params.Memory, h.params.Parallelism, h.params.TagLength,
	)
	return encodeHash(h.params, salt, tag), nil
}

// Verify reports whether password produced encoded.
//
// It returns nil on a match, ErrPasswordMismatch on a mismatch, and
// ErrMalformedHash or ErrUnsupportedHash when encoded cannot be read at all.
// Those last two are not failed logins and must never be reported as though
// they were: they mean a stored credential is unreadable, which is the
// operator's problem rather than that of the person trying to sign in.
//
// This is a function and not a method because verification is defined entirely
// by the hash. Applying a Hasher's current parameters here would verify every
// older credential at the wrong cost and get the wrong answer for all of them.
func Verify(encoded, password string) error {
	params, salt, want, err := decodeHash(encoded)
	if err != nil {
		return err
	}

	got := argon2.IDKey(
		[]byte(password), salt,
		params.Iterations, params.Memory, params.Parallelism, params.TagLength,
	)

	// Constant time, so that how many leading bytes happened to match is not
	// readable from how long the comparison took.
	if subtle.ConstantTimeCompare(got, want) != 1 {
		return ErrPasswordMismatch
	}
	return nil
}

// NeedsRehash reports whether encoded was minted at a different cost than this
// Hasher mints at now, and should therefore be replaced the next time the
// password is known — which is during a successful login, and nowhere else.
//
// Any difference counts, not only a weaker one. The Hasher is current policy,
// and a cost that was deliberately lowered is still the cost that should apply.
func (h Hasher) NeedsRehash(encoded string) (bool, error) {
	params, _, _, err := decodeHash(encoded)
	if err != nil {
		return false, err
	}
	return params != h.params, nil
}

// decoyInput is what VerifyDecoy checks against a hash nothing matches. Its
// value is irrelevant; only the work of hashing it is wanted.
const decoyInput = "nusa decoy"

// VerifyDecoy performs one complete verification against a hash whose input
// nobody knows, and discards the result.
//
// It exists for one reason: an attacker with a stopwatch must not be able to
// tell a registered address from an unregistered one. A login handler that
// returns early when there is no account answers in microseconds while a real
// account costs a full Argon2id run, and that gap is a reliable
// account-enumeration oracle no matter how carefully the error messages are
// worded. Calling this on the no-such-account path makes both paths cost the
// same work.
//
// It uses this Hasher's parameters, so it matches every credential minted since
// the last parameter change. A password still stored at an older cost will
// differ, which is a far smaller leak than the one this closes and one that
// rehash-on-login shrinks over time.
//
// There is no return value because there is no outcome. Nothing about the
// result may reach the caller, or the timing goes straight back in through the
// branch that reads it.
func (h Hasher) VerifyDecoy() {
	_ = Verify(h.decoy, decoyInput)
}

// newDecoy builds an encoded hash with p's costs, a random salt and a random
// tag.
//
// The tag is random rather than derived, and that is what makes it
// unmatchable: no input produces it, so VerifyDecoy cannot succeed by accident.
// Building it costs no Argon2id run, because only the parameters and the
// lengths affect how long verifying it takes.
func newDecoy(p Params) (string, error) {
	salt := make([]byte, p.SaltLength)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("draw decoy salt: %w", err)
	}
	tag := make([]byte, p.TagLength)
	if _, err := rand.Read(tag); err != nil {
		return "", fmt.Errorf("draw decoy tag: %w", err)
	}
	return encodeHash(p, salt, tag), nil
}

// b64 is the encoding the PHC string format specifies: the standard base64
// alphabet, without padding.
var b64 = base64.RawStdEncoding

func encodeHash(p Params, salt, tag []byte) string {
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2Version, p.Memory, p.Iterations, p.Parallelism,
		b64.EncodeToString(salt), b64.EncodeToString(tag),
	)
}

// decodeHash reads a PHC-format Argon2id string back into its parts.
//
// It is strict about all of it: field count, algorithm name, version, the order
// and spelling of the cost parameters, and the base64 alphabet. A hash that is
// nearly right is refused rather than repaired, because the only thing worse
// than failing to verify a credential is verifying it against parameters that
// were guessed.
func decodeHash(encoded string) (Params, []byte, []byte, error) {
	// A PHC string opens with '$', so the split has an empty leading field.
	fields := strings.Split(encoded, "$")
	if len(fields) != 6 || fields[0] != "" {
		return Params{}, nil, nil, fmt.Errorf("%w: expected 5 fields, found %d", ErrMalformedHash, len(fields)-1)
	}

	if fields[1] != "argon2id" {
		return Params{}, nil, nil, fmt.Errorf("%w: algorithm %q is not argon2id", ErrUnsupportedHash, fields[1])
	}

	var version int
	if _, err := fmt.Sscanf(fields[2], "v=%d", &version); err != nil {
		return Params{}, nil, nil, fmt.Errorf("%w: unreadable version %q", ErrMalformedHash, fields[2])
	}
	if version != argon2Version {
		return Params{}, nil, nil, fmt.Errorf("%w: argon2 version %d, want %d", ErrUnsupportedHash, version, argon2Version)
	}

	var p Params
	if _, err := fmt.Sscanf(fields[3], "m=%d,t=%d,p=%d", &p.Memory, &p.Iterations, &p.Parallelism); err != nil {
		return Params{}, nil, nil, fmt.Errorf("%w: unreadable parameters %q", ErrMalformedHash, fields[3])
	}
	if p.Memory > MaxVerifiableMemory {
		return Params{}, nil, nil, fmt.Errorf("%w: hash asks for %d KiB, ceiling is %d KiB",
			ErrUnsupportedHash, p.Memory, MaxVerifiableMemory)
	}
	if p.Parallelism < 1 || uint32(p.Parallelism)*8 > p.Memory {
		return Params{}, nil, nil, fmt.Errorf("%w: %d lanes are not possible in %d KiB",
			ErrUnsupportedHash, p.Parallelism, p.Memory)
	}
	if p.Iterations < 1 {
		return Params{}, nil, nil, fmt.Errorf("%w: iterations must be at least 1", ErrUnsupportedHash)
	}

	salt, err := b64.DecodeString(fields[4])
	if err != nil {
		return Params{}, nil, nil, fmt.Errorf("%w: unreadable salt: %w", ErrMalformedHash, err)
	}
	tag, err := b64.DecodeString(fields[5])
	if err != nil {
		return Params{}, nil, nil, fmt.Errorf("%w: unreadable tag: %w", ErrMalformedHash, err)
	}
	if len(salt) == 0 || len(tag) == 0 {
		return Params{}, nil, nil, fmt.Errorf("%w: empty salt or tag", ErrMalformedHash)
	}
	// Bounded for the same reason the memory figure is: these lengths come
	// from storage, and a hash claiming a megabyte of salt is not a credential
	// worth attempting. Keeping them provably small is also what makes the
	// conversions below safe on every architecture.
	if len(salt) > maxSaltOrTagLength || len(tag) > maxSaltOrTagLength {
		return Params{}, nil, nil, fmt.Errorf("%w: salt or tag longer than %d bytes",
			ErrUnsupportedHash, maxSaltOrTagLength)
	}

	// The lengths are a property of the encoded value rather than a separate
	// claim about it, so they are read off instead of parsed.
	//
	// G115 is suppressed on exactly these two lines and nowhere else: the
	// check immediately above proves both are at most maxSaltOrTagLength, and
	// gosec's rule is syntactic so it cannot see that. Removing the bound
	// check would make this suppression a lie, which is why the two sit
	// together.
	p.SaltLength = uint32(len(salt)) //nolint:gosec // bounded by maxSaltOrTagLength above
	p.TagLength = uint32(len(tag))   //nolint:gosec // bounded by maxSaltOrTagLength above

	return p, salt, tag, nil
}
