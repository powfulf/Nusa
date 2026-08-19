// SPDX-License-Identifier: MIT

package ledger

import (
	"cmp"
	"encoding/json"
	"fmt"
	"time"
)

// dateLayout is ISO 8601's calendar date, the only form this package reads or
// writes.
const dateLayout = "2006-01-02"

// Date is a civil calendar date: a year, a month and a day, with no time of
// day and no timezone.
//
// This is the only thing that decides which day a transaction falls on, which
// balance a date-bounded report includes, and in what order lots are consumed.
// A Transaction also records the instant it happened and the zone it happened
// in, but those are for display and are never read by a calculation.
//
// The separation is deliberate. A timestamp plus a zone can put the same
// purchase in two different months depending on where the reader is sitting,
// and a balance that changes when a household member flies to another country
// is a balance nobody can reconcile. The user wrote down a day; the day is
// what we keep.
//
// The zero Date is not a real date. Use NewDate or ParseDate.
type Date struct {
	year  int
	month time.Month
	day   int
}

// NewDate returns the given calendar date, rejecting days that do not exist.
//
// Unlike time.Date it does not normalise: 30 February is an error, not 1 or 2
// March. A date arriving from a user or an importer is a claim about what
// happened, and quietly moving it is how a transaction ends up in the wrong
// month.
func NewDate(year int, month time.Month, day int) (Date, error) {
	if year < 1 || year > 9999 {
		return Date{}, fmt.Errorf("%w: year %d is outside 1..9999", ErrInvalidDate, year)
	}
	if month < time.January || month > time.December {
		return Date{}, fmt.Errorf("%w: month %d is outside 1..12", ErrInvalidDate, int(month))
	}
	if day < 1 {
		return Date{}, fmt.Errorf("%w: day %d is before the first of the month", ErrInvalidDate, day)
	}
	// Round-tripping through time.Date is how the leap-year rules stay in one
	// place: if normalisation moved anything, the date did not exist.
	normalised := time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
	if normalised.Year() != year || normalised.Month() != month || normalised.Day() != day {
		return Date{}, fmt.Errorf("%w: %d-%02d-%02d does not exist", ErrInvalidDate, year, int(month), day)
	}
	return Date{year: year, month: month, day: day}, nil
}

// ParseDate reads a date written as YYYY-MM-DD.
func ParseDate(s string) (Date, error) {
	t, err := time.Parse(dateLayout, s)
	if err != nil {
		return Date{}, fmt.Errorf("%w: %q is not a YYYY-MM-DD date", ErrInvalidDate, s)
	}
	return NewDate(t.Year(), t.Month(), t.Day())
}

// DateOf returns the civil date an instant falls on in the given location.
//
// This is the one sanctioned bridge from a timestamp to a date, and it demands
// a location so that the caller has to say whose day they mean. Nothing in
// this package calls it; it exists for the layers that receive a timestamp
// from a user or an importer and must decide, once, which day that was.
func DateOf(t time.Time, loc *time.Location) (Date, error) {
	if loc == nil {
		return Date{}, fmt.Errorf("%w: no location given for %s", ErrInvalidDate, t.Format(time.RFC3339))
	}
	local := t.In(loc)
	return NewDate(local.Year(), local.Month(), local.Day())
}

// Year returns the calendar year.
func (d Date) Year() int { return d.year }

// Month returns the calendar month.
func (d Date) Month() time.Month { return d.month }

// Day returns the day of the month.
func (d Date) Day() int { return d.day }

// IsZero reports whether the date is the unset zero value.
func (d Date) IsZero() bool { return d.year == 0 }

// String renders the date as YYYY-MM-DD.
func (d Date) String() string {
	if d.IsZero() {
		return "<no date>"
	}
	return fmt.Sprintf("%04d-%02d-%02d", d.year, int(d.month), d.day)
}

// Compare returns -1 if d is earlier than other, +1 if later, 0 if the same
// day. The zero date sorts before every real one.
func (d Date) Compare(other Date) int {
	switch {
	case d.year != other.year:
		return cmp.Compare(d.year, other.year)
	case d.month != other.month:
		return cmp.Compare(d.month, other.month)
	default:
		return cmp.Compare(d.day, other.day)
	}
}

// Before reports whether d falls earlier than other.
func (d Date) Before(other Date) bool { return d.Compare(other) < 0 }

// After reports whether d falls later than other.
func (d Date) After(other Date) bool { return d.Compare(other) > 0 }

// Equal reports whether both values are the same day.
func (d Date) Equal(other Date) bool { return d == other }

// AddDays returns the date n days later, or earlier when n is negative.
func (d Date) AddDays(n int) (Date, error) {
	if d.IsZero() {
		return Date{}, fmt.Errorf("%w: cannot add days to an unset date", ErrInvalidDate)
	}
	shifted := time.Date(d.year, d.month, d.day+n, 0, 0, 0, 0, time.UTC)
	return NewDate(shifted.Year(), shifted.Month(), shifted.Day())
}

// MarshalJSON writes the date as a YYYY-MM-DD string.
func (d Date) MarshalJSON() ([]byte, error) {
	if d.IsZero() {
		return nil, fmt.Errorf("%w: unset", ErrInvalidDate)
	}
	return json.Marshal(d.String())
}

// UnmarshalJSON reads a YYYY-MM-DD string.
func (d *Date) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return fmt.Errorf("decode date: %w", err)
	}
	parsed, err := ParseDate(s)
	if err != nil {
		return err
	}
	*d = parsed
	return nil
}
