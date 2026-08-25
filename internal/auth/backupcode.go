// SPDX-License-Identifier: AGPL-3.0-only

package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"strings"
)

// Backup codes are hashed with SHA-256 while passwords are hashed with
// Argon2id, and the difference is deliberate. This is the first question a
// reviewer asks about this file, so here is the answer, at the place the
// question occurs.
//
// Argon2id is slow on purpose, and what that slowness buys is protection for
// low-entropy secrets. A password is chosen by a person, which means it is
// drawn from a distribution an attacker can enumerate — a wordlist, a keyboard
// pattern, a name and a year. Against a candidate set of maybe 2^30 plausible
// guesses, the only defence is making each guess expensive.
//
// A backup code is not chosen by anybody. It is drawn from a cryptographic
// random source at a size fixed below, so there is no distribution to
// enumerate and no wordlist to try — an attacker has nothing better than the
// full space. Argon2id would buy nothing there and would cost something real:
// verifying a code means comparing against every unspent code a user holds, so
// one submission would run ten memory-hard hashes and hand an attacker a
// ten-times amplified way to exhaust the server's memory.
//
// That argument is only sound if the entropy is genuinely past brute force, so
// it is sized rather than asserted. See backupCodeBytes.

// backupCodeBytes is 10, giving each code 80 bits of entropy.
//
// The number is chosen against the attack SHA-256 actually permits: an attacker
// holding the database enumerates the space offline at whatever rate their
// hardware manages. Ten thousand million SHA-256 evaluations per second is
// within reach of a modest GPU rig, so:
//
//	40 bits  ~ 2 minutes        — worthless
//	50 bits  ~ 1.3 days         — weak
//	64 bits  ~ 58 years         — sufficient
//	80 bits  ~ 3.8 million years — the choice here
//
// Eighty bits is also exactly sixteen base32 characters, which is four groups
// of four: long enough to be safe, short enough that somebody will actually
// write it on paper and type it back a year later. Shortening this without
// moving to a slow hash would quietly invalidate the reasoning above.
const backupCodeBytes = 10

// BackupCodeCount is how many codes are issued at once. Ten is enough to
// survive losing a phone more than once without being a list nobody stores
// carefully.
const BackupCodeCount = 10

// backupCodeGroup is how many characters appear between separators. Grouping
// exists only to be read aloud and typed back; MatchBackupCode ignores it.
const backupCodeGroup = 4

// NewBackupCodes draws a fresh set of single-use recovery codes.
//
// The codes are returned in the form they should be shown to the user, grouped
// with hyphens. They are returned in clear because this is the only moment they
// exist in clear: the caller shows them once and stores nothing but their
// hashes.
func NewBackupCodes() ([]string, error) {
	codes := make([]string, BackupCodeCount)
	for i := range codes {
		raw := make([]byte, backupCodeBytes)
		if _, err := rand.Read(raw); err != nil {
			return nil, fmt.Errorf("draw backup code: %w", err)
		}
		codes[i] = groupBackupCode(base32NoPad.EncodeToString(raw))
	}
	return codes, nil
}

func groupBackupCode(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i += backupCodeGroup {
		if i > 0 {
			b.WriteByte('-')
		}
		b.WriteString(s[i:min(i+backupCodeGroup, len(s))])
	}
	return b.String()
}

// normaliseBackupCode strips the presentation and leaves the value.
//
// A code is transcribed by a human from paper, so the hyphens may be absent or
// in the wrong places, the case may be anything, and there will be stray
// spaces. None of that is part of the secret. This mirrors DecodeSecret and
// differs from the ledger's refusal to tidy user text for the same reason: this
// is a machine value with one valid reading, not something somebody wrote.
func normaliseBackupCode(code string) string {
	return strings.ToUpper(strings.Map(func(r rune) rune {
		switch r {
		case '-', ' ', '\t', '\n', '\r':
			return -1
		}
		return r
	}, code))
}

// HashBackupCode returns the stored form of a backup code: lowercase hex of the
// SHA-256 of its normalised text.
//
// It is unsalted, and that is safe here for the same reason the fast hash is:
// at 80 bits there is no precomputed table to defend against, because no table
// covers a space that size. Being unsalted also means a code hashes to the same
// value every time, which is what allows a comparison rather than a
// verification.
func HashBackupCode(code string) string {
	sum := sha256.Sum256([]byte(normaliseBackupCode(code)))
	return hex.EncodeToString(sum[:])
}

// MatchBackupCode reports which of hashes the presented code corresponds to.
//
// It returns the index of the match so the caller can mark exactly that code
// spent. A backup code is single use; a caller that ignores the index and
// merely records "a code was used" has implemented a code that works forever.
//
// The scan does not stop at the first match. Comparing every entry costs ten
// fixed-size comparisons and removes the correlation between how long the check
// took and where in the list the code sat — which, over a few attempts, is how
// many of a user's codes are still unspent.
func MatchBackupCode(hashes []string, code string) (int, error) {
	if strings.TrimSpace(code) == "" {
		return 0, fmt.Errorf("%w: empty", ErrInvalidBackupCode)
	}

	presented := HashBackupCode(code)

	matched := -1
	for i, stored := range hashes {
		if subtle.ConstantTimeCompare([]byte(presented), []byte(stored)) == 1 {
			matched = i
		}
	}

	if matched < 0 {
		return 0, ErrInvalidBackupCode
	}
	return matched, nil
}
