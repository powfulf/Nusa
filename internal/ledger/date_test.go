// SPDX-License-Identifier: MIT

package ledger_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/GaffaQ/Nusa/internal/ledger"
)

// time.Date turns 30 February into 1 or 2 March without complaint. A date is a
// claim about what happened, so this package refuses instead of relocating it.
func TestNewDateDoesNotNormalise(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		year  int
		month time.Month
		day   int
	}{
		{"the thirtieth of February", 2024, time.February, 30},
		{"the twenty-ninth of February in a common year", 2023, time.February, 29},
		{"the thirty-first of April", 2024, time.April, 31},
		{"day zero", 2024, time.January, 0},
		{"month zero", 2024, 0, 15},
		{"month thirteen", 2024, 13, 1},
		{"year zero", 0, time.January, 1},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := ledger.NewDate(tc.year, tc.month, tc.day)
			require.ErrorIs(t, err, ledger.ErrInvalidDate)
		})
	}
}

func TestNewDateAcceptsALeapDay(t *testing.T) {
	t.Parallel()

	d, err := ledger.NewDate(2024, time.February, 29)
	require.NoError(t, err)
	require.Equal(t, "2024-02-29", d.String())
}

func TestParseDate(t *testing.T) {
	t.Parallel()

	d, err := ledger.ParseDate("2024-03-17")
	require.NoError(t, err)
	require.Equal(t, 2024, d.Year())
	require.Equal(t, time.March, d.Month())
	require.Equal(t, 17, d.Day())

	for _, raw := range []string{"", "17/03/2024", "2024-3-17", "2024-02-30", "2024-03-17T00:00:00Z"} {
		_, err := ledger.ParseDate(raw)
		require.ErrorIs(t, err, ledger.ErrInvalidDate, "should have rejected %q", raw)
	}
}

func TestDateCompare(t *testing.T) {
	t.Parallel()

	earlier := mustDate(t, "2024-03-17")
	later := mustDate(t, "2024-03-18")
	nextMonth := mustDate(t, "2024-04-01")
	nextYear := mustDate(t, "2025-01-01")

	require.True(t, earlier.Before(later))
	require.True(t, later.After(earlier))
	require.True(t, earlier.Equal(mustDate(t, "2024-03-17")))
	require.Equal(t, 0, earlier.Compare(mustDate(t, "2024-03-17")))
	require.Equal(t, -1, earlier.Compare(nextMonth))
	require.Equal(t, -1, nextMonth.Compare(nextYear))
	require.Equal(t, 1, nextYear.Compare(earlier))
}

func TestDateAddDays(t *testing.T) {
	t.Parallel()

	// Across a leap day, so the arithmetic is not just adding to the day.
	shifted, err := mustDate(t, "2024-02-28").AddDays(2)
	require.NoError(t, err)
	require.Equal(t, "2024-03-01", shifted.String())

	back, err := shifted.AddDays(-2)
	require.NoError(t, err)
	require.Equal(t, "2024-02-28", back.String())

	_, err = ledger.Date{}.AddDays(1)
	require.ErrorIs(t, err, ledger.ErrInvalidDate)
}

// The one sanctioned bridge from an instant to a day. It demands a location so
// that the caller has to say whose day they mean — the same instant is two
// different dates either side of midnight.
func TestDateOfNeedsALocation(t *testing.T) {
	t.Parallel()

	instant := time.Date(2024, time.March, 17, 20, 30, 0, 0, time.UTC)

	inUTC, err := ledger.DateOf(instant, time.UTC)
	require.NoError(t, err)
	require.Equal(t, "2024-03-17", inUTC.String())

	// The same instant is already the eighteenth in Jakarta.
	inJakarta, err := ledger.DateOf(instant, jakarta)
	require.NoError(t, err)
	require.Equal(t, "2024-03-18", inJakarta.String())

	_, err = ledger.DateOf(instant, nil)
	require.ErrorIs(t, err, ledger.ErrInvalidDate)
}

func TestDateJSONRoundTrip(t *testing.T) {
	t.Parallel()

	original := mustDate(t, "2024-03-17")

	encoded, err := json.Marshal(original)
	require.NoError(t, err)
	require.Equal(t, `"2024-03-17"`, string(encoded))

	var decoded ledger.Date
	require.NoError(t, json.Unmarshal(encoded, &decoded))
	require.True(t, original.Equal(decoded))

	var invalid ledger.Date
	require.Error(t, json.Unmarshal([]byte(`"2024-02-30"`), &invalid))

	_, err = json.Marshal(ledger.Date{})
	require.Error(t, err)
}

func TestZeroDate(t *testing.T) {
	t.Parallel()

	var unset ledger.Date

	require.True(t, unset.IsZero())
	require.Equal(t, "<no date>", unset.String())
	require.True(t, unset.Before(mustDate(t, "2024-03-17")))
}
