// SPDX-License-Identifier: AGPL-3.0-only

package auth_test

import (
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/powfulf/Nusa/internal/auth"
)

// What these guards must cover. Written before they were, and each item was
// broken on purpose and watched failing afterwards (CLAUDE.md §11).
//
//	1. A set is BackupCodeCount codes, all distinct, in the documented shape.
//	2. Codes carry the entropy the SHA-256 decision is justified by.
//	3. Hashing is stable, and normalises how a person transcribes a code.
//	4. A code matches its own hash and no other.
//	5. Match returns which code matched, so the caller can spend that one.
//	6. A wrong, empty or absent code is refused.
//	7. Two independently drawn sets do not overlap.

var backupCodeShape = regexp.MustCompile(`^[A-Z2-7]{4}-[A-Z2-7]{4}-[A-Z2-7]{4}-[A-Z2-7]{4}$`)

// 1, 2.
//
// The count is checked against BackupCodeCount rather than against a literal
// ten, so what it proves is that the generator honours its own constant — not
// that the constant is any particular number. Lowering BackupCodeCount is a
// policy change and this test will follow it, deliberately; the name says
// "the stated size" rather than "ten" for that reason.
func TestABackupCodeSetIsDistinctCodesOfTheStatedSize(t *testing.T) {
	t.Parallel()

	codes, err := auth.NewBackupCodes()
	require.NoError(t, err)
	require.Len(t, codes, auth.BackupCodeCount)

	seen := map[string]bool{}
	for _, code := range codes {
		require.Regexp(t, backupCodeShape, code,
			"a code is four groups of four base32 characters")

		// 16 base32 characters is 80 bits, which is the number the decision to
		// hash these with SHA-256 rather than Argon2id rests on. Shortening
		// the code without revisiting that decision is the mistake this line
		// exists to stop, so it is asserted rather than left to the comment.
		require.Len(t, strings.ReplaceAll(code, "-", ""), 16,
			"80 bits of entropy is what makes a fast hash defensible here")

		require.False(t, seen[code], "a set must not contain the same code twice")
		seen[code] = true
	}
}

// 7.
func TestTwoSetsDoNotOverlap(t *testing.T) {
	t.Parallel()

	first, err := auth.NewBackupCodes()
	require.NoError(t, err)
	second, err := auth.NewBackupCodes()
	require.NoError(t, err)

	seen := map[string]bool{}
	for _, code := range first {
		seen[code] = true
	}
	for _, code := range second {
		require.False(t, seen[code],
			"a code repeated across two draws means the source is not random")
	}
}

// 3.
func TestHashingIsStableAndIgnoresHowACodeWasTranscribed(t *testing.T) {
	t.Parallel()

	codes, err := auth.NewBackupCodes()
	require.NoError(t, err)
	code := codes[0]

	require.Equal(t, auth.HashBackupCode(code), auth.HashBackupCode(code),
		"the same code must hash the same way every time, or nothing can be stored")

	bare := strings.ReplaceAll(code, "-", "")
	for name, written := range map[string]string{
		"as printed":        code,
		"no hyphens":        bare,
		"lowercase":         strings.ToLower(code),
		"spaces not dashes": strings.ReplaceAll(code, "-", " "),
		"surrounding space": "  " + code + "  ",
		"regrouped":         bare[:8] + "-" + bare[8:],
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, auth.HashBackupCode(code), auth.HashBackupCode(written),
				"a person retyping a code from paper must not be locked out by punctuation")
		})
	}

	// Different codes must not collide, which is the other half of the same
	// statement: normalisation may remove presentation and nothing else.
	require.NotEqual(t, auth.HashBackupCode(codes[0]), auth.HashBackupCode(codes[1]))

	// SHA-256 hex is 64 characters.
	require.Len(t, auth.HashBackupCode(code), 64)
	require.Regexp(t, `^[0-9a-f]{64}$`, auth.HashBackupCode(code))
}

// 4, 5, 6.
func TestMatchingSaysWhichCodeWasUsed(t *testing.T) {
	t.Parallel()

	codes, err := auth.NewBackupCodes()
	require.NoError(t, err)

	hashes := make([]string, len(codes))
	for i, code := range codes {
		hashes[i] = auth.HashBackupCode(code)
	}

	// Every code must resolve to its own position. Checking only one would
	// pass for an implementation that always returned the first index.
	for want, code := range codes {
		got, err := auth.MatchBackupCode(hashes, code)
		require.NoError(t, err)
		require.Equal(t, want, got,
			"code %d resolved to position %d, so the wrong one would be spent", want, got)
	}

	// And through the transcription a person actually produces.
	got, err := auth.MatchBackupCode(hashes, strings.ToLower(strings.ReplaceAll(codes[7], "-", "")))
	require.NoError(t, err)
	require.Equal(t, 7, got)

	t.Run("a code not in the set", func(t *testing.T) {
		t.Parallel()
		other, err := auth.NewBackupCodes()
		require.NoError(t, err)
		_, err = auth.MatchBackupCode(hashes, other[0])
		require.ErrorIs(t, err, auth.ErrInvalidBackupCode)
	})

	t.Run("a spent code is simply absent", func(t *testing.T) {
		t.Parallel()
		// Spending is the caller removing the hash. Once it is gone the code
		// is refused by the same path as one that never existed, which is why
		// there is no separate error for it.
		remaining := append(append([]string{}, hashes[:4]...), hashes[5:]...)
		_, err := auth.MatchBackupCode(remaining, codes[4])
		require.ErrorIs(t, err, auth.ErrInvalidBackupCode)

		still, err := auth.MatchBackupCode(remaining, codes[5])
		require.NoError(t, err, "the other codes must keep working")
		require.Equal(t, 4, still, "and their positions shift with the slice")
	})

	for name, code := range map[string]string{
		"empty":      "",
		"whitespace": "   ",
		"nonsense":   "ZZZZ-ZZZZ-ZZZZ-ZZZZ",
		"truncated":  codes[0][:9],
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := auth.MatchBackupCode(hashes, code)
			require.ErrorIs(t, err, auth.ErrInvalidBackupCode)
		})
	}

	t.Run("no codes left at all", func(t *testing.T) {
		t.Parallel()
		_, err := auth.MatchBackupCode(nil, codes[0])
		require.ErrorIs(t, err, auth.ErrInvalidBackupCode)
	})

	// The explicit refusal of an empty code is only reachable through a stored
	// set that contains the hash of an empty string, which NewBackupCodes
	// cannot produce. It is constructed here on purpose: without this case the
	// check in MatchBackupCode is unfalsifiable — removing it changes no test,
	// because an empty code fails to match anything anyway — and an
	// unfalsifiable check is one nobody can tell is working.
	t.Run("an empty code never matches, even against a hash of nothing", func(t *testing.T) {
		t.Parallel()
		poisoned := []string{auth.HashBackupCode("")}
		_, err := auth.MatchBackupCode(poisoned, "")
		require.ErrorIs(t, err, auth.ErrInvalidBackupCode)
		_, err = auth.MatchBackupCode(poisoned, "   ")
		require.ErrorIs(t, err, auth.ErrInvalidBackupCode)
	})
}
