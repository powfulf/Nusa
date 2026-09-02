// SPDX-License-Identifier: AGPL-3.0-only

package api_test

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/powfulf/Nusa/internal/api"
	"github.com/powfulf/Nusa/internal/ledger"
)

// What these guards must cover, decided before any of them was written.
//
//  1. A position survives the round trip exactly.
//  2. The token is opaque: nothing about it invites a client to read or build
//     one, and it survives a URL unescaped.
//  3. A cursor presented under different filters is refused, not reinterpreted.
//  4. Two filter sets that name the same days are the same filter set, however
//     they were spelled.
//  5. An unknown version is refused rather than decoded under current rules.
//  6. A malformed token is refused in every way it can be malformed: not
//     base64, wrong field count, bad date, bad identity.
//  7. A refusal never yields a usable position, so a caller that ignores the
//     error cannot page from a guess.

func someDate(t *testing.T, s string) ledger.Date {
	t.Helper()
	d, err := ledger.ParseDate(s)
	require.NoError(t, err)
	return d
}

const cursorTestID = "0195c0de-0001-7000-8000-000000000001"

func TestACursorRoundTripsExactly(t *testing.T) {
	t.Parallel()

	f := api.TransactionFiltersForTest(someDate(t, "2026-01-01"), someDate(t, "2026-12-31"))
	token := api.EncodeCursorForTest(f, someDate(t, "2026-03-07"), cursorTestID)

	date, id, err := api.DecodeCursorForTest(f, token)
	require.NoError(t, err)
	require.Equal(t, "2026-03-07", date.String())
	require.Equal(t, cursorTestID, string(id))
}

func TestACursorIsOpaqueAndURLSafe(t *testing.T) {
	t.Parallel()

	f := api.TransactionFiltersForTest(ledger.Date{}, ledger.Date{})
	token := api.EncodeCursorForTest(f, someDate(t, "2026-03-07"), cursorTestID)

	// Nothing a client would think to read or edit. The point is not secrecy —
	// it is not signed and is not a secret — but that the shape of a page
	// position stays ours to change.
	require.NotContains(t, token, "2026")
	require.NotContains(t, token, cursorTestID)

	// base64url without padding: no '+', '/' or '=' to escape, so a cursor can
	// travel in a query string untouched.
	require.False(t, strings.ContainsAny(token, "+/="), "token needs URL escaping: %q", token)
}

func TestACursorFromAnotherFilterSetIsRefused(t *testing.T) {
	t.Parallel()

	narrow := api.TransactionFiltersForTest(someDate(t, "2026-03-01"), someDate(t, "2026-03-31"))
	wide := api.TransactionFiltersForTest(someDate(t, "2026-01-01"), someDate(t, "2026-12-31"))

	token := api.EncodeCursorForTest(narrow, someDate(t, "2026-03-07"), cursorTestID)

	// Reinterpreting it would hand back a page that looks perfectly ordinary
	// and silently omits everything the two filter sets disagree about.
	_, _, err := api.DecodeCursorForTest(wide, token)
	require.ErrorIs(t, err, api.ErrCursorFilterChangedForTest)

	// Dropping a bound is a change too, in both directions.
	_, _, err = api.DecodeCursorForTest(
		api.TransactionFiltersForTest(someDate(t, "2026-03-01"), ledger.Date{}), token)
	require.ErrorIs(t, err, api.ErrCursorFilterChangedForTest)
}

func TestFiltersNamingTheSameDaysAreTheSameFilterSet(t *testing.T) {
	t.Parallel()

	// The digest is built from the canonical form, so a client that writes its
	// dates differently between two requests is not told its filters changed
	// when they did not.
	first := api.TransactionFiltersForTest(someDate(t, "2026-03-01"), someDate(t, "2026-03-31"))
	second := api.TransactionFiltersForTest(someDate(t, "2026-03-01"), someDate(t, "2026-03-31"))

	token := api.EncodeCursorForTest(first, someDate(t, "2026-03-07"), cursorTestID)
	_, _, err := api.DecodeCursorForTest(second, token)
	require.NoError(t, err)
}

func TestACursorFromAnotherVersionIsRefused(t *testing.T) {
	t.Parallel()

	f := api.TransactionFiltersForTest(ledger.Date{}, ledger.Date{})

	// A cursor outlives the code that made it: a client can hold one across a
	// deployment. Decoding old bytes under new rules lands somewhere plausible
	// and wrong, which is worse than refusing.
	old := base64.RawURLEncoding.EncodeToString(
		[]byte("v0\x1f" + "00000000" + "\x1f2026-03-07\x1f" + cursorTestID))

	_, _, err := api.DecodeCursorForTest(f, old)
	require.Error(t, err)
	require.Contains(t, err.Error(), "version")
}

func TestAMalformedCursorIsRefusedAndYieldsNoPosition(t *testing.T) {
	t.Parallel()

	f := api.TransactionFiltersForTest(ledger.Date{}, ledger.Date{})
	enc := func(s string) string { return base64.RawURLEncoding.EncodeToString([]byte(s)) }

	for name, token := range map[string]string{
		"not base64": "!!!!not base64!!!!",
		"empty":      "",
		// The digest is the real one in both of these, so the field count is the
		// only thing wrong. A token carrying a bad digest as well would be refused
		// for that instead, and the count rule would go untested while looking
		// guarded.
		"too few fields": enc("v1\x1f" + api.DigestForTest(f) + "\x1f2026-03-07"),
		"too many fields": enc("v1\x1f" + api.DigestForTest(f) +
			"\x1f2026-03-07\x1f" + cursorTestID + "\x1fextra"),
		"impossible date":  enc("v1\x1f" + api.DigestForTest(f) + "\x1f2026-02-30\x1f" + cursorTestID),
		"unparseable date": enc("v1\x1f" + api.DigestForTest(f) + "\x1fyesterday\x1f" + cursorTestID),
		"identity not a uuid": enc("v1\x1f" + api.DigestForTest(f) +
			"\x1f2026-03-07\x1fnot-a-uuid"),
		"identity is uuid4": enc("v1\x1f" + api.DigestForTest(f) +
			"\x1f2026-03-07\x1f0195c0de-0001-4000-8000-000000000001"),
		"identity uppercase": enc("v1\x1f" + api.DigestForTest(f) +
			"\x1f2026-03-07\x1f0195C0DE-0001-7000-8000-000000000001"),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			date, id, err := api.DecodeCursorForTest(f, token)
			require.Error(t, err)
			// A caller that ignores the error must not be able to page from a
			// half-decoded position.
			require.True(t, date.IsZero(), "a refused cursor yielded a date")
			require.Empty(t, id, "a refused cursor yielded an identity")
		})
	}
}
