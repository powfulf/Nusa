// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"fmt"
	"math/big"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/GaffaQ/Nusa/internal/ledger"
)

// Every conversion between a domain value and a database row lives here, and
// every one of them is exact. Nothing in this file rounds, truncates, or
// widens through a float — an amount that survives a round trip changed by one
// smallest unit is a wrong balance, and a wrong balance found a year later is
// indistinguishable from theft.

// uuidFrom parses a domain identity into the column type.
//
// The domain has already validated the identity as a canonical lowercase
// UUIDv7, and the database checks the version again. This only has to fail
// loudly when handed something that is not a uuid at all.
func uuidFrom(id string) (pgtype.UUID, error) {
	var u pgtype.UUID
	if id == "" {
		return u, nil // NULL: an absent parent, an absent reversal link
	}
	if err := u.Scan(id); err != nil {
		return pgtype.UUID{}, fmt.Errorf("%w: %q is not a uuid", ErrInvalidWrite, id)
	}
	return u, nil
}

const hexDigits = "0123456789abcdef"

// uuidTo renders a column value as the canonical lowercase form the domain
// requires. It is written out rather than delegated because the domain
// compares identities as exact strings, so one identity in two spellings would
// look like two identities.
func uuidTo(u pgtype.UUID) string {
	if !u.Valid {
		return ""
	}
	out := make([]byte, 0, 36)
	for i, b := range u.Bytes {
		if i == 4 || i == 6 || i == 8 || i == 10 {
			out = append(out, '-')
		}
		out = append(out, hexDigits[b>>4], hexDigits[b&0x0f])
	}
	return string(out)
}

// numericFrom writes a whole number into a numeric column.
func numericFrom(v *big.Int) pgtype.Numeric {
	if v == nil {
		return pgtype.Numeric{}
	}
	// The value is copied: pgtype keeps the pointer, and a caller holding the
	// original must not be able to change what was written.
	return pgtype.Numeric{Int: new(big.Int).Set(v), Exp: 0, Valid: true}
}

// numericTo reads a numeric column back as an exact whole number.
//
// PostgreSQL is free to hand back the same value with a different exponent —
// 150000 may arrive as 15 with an exponent of 4 — so the exponent is applied
// rather than assumed to be zero. A negative exponent means the column held a
// fraction, which none of the columns using this are allowed to; it is
// reported instead of being rounded away.
func numericTo(n pgtype.Numeric) (*big.Int, error) {
	switch {
	case !n.Valid:
		return nil, fmt.Errorf("%w: numeric is null", ErrCorrupt)
	case n.NaN:
		return nil, fmt.Errorf("%w: numeric is NaN", ErrCorrupt)
	case n.InfinityModifier != pgtype.Finite:
		return nil, fmt.Errorf("%w: numeric is infinite", ErrCorrupt)
	case n.Int == nil:
		return nil, fmt.Errorf("%w: numeric has no digits", ErrCorrupt)
	}

	value := new(big.Int).Set(n.Int)
	switch {
	case n.Exp > 0:
		scale := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(n.Exp)), nil)
		value.Mul(value, scale)
	case n.Exp < 0:
		scale := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(-n.Exp)), nil)
		quotient, remainder := new(big.Int).QuoRem(value, scale, new(big.Int))
		if remainder.Sign() != 0 {
			return nil, fmt.Errorf("%w: numeric %s is not a whole number", ErrCorrupt, n.Int)
		}
		value = quotient
	}
	return value, nil
}

// moneyFrom splits a Money into the two columns section 4.4 requires.
func moneyFrom(m ledger.Money) (pgtype.Numeric, string, error) {
	if !m.IsValid() {
		return pgtype.Numeric{}, "", fmt.Errorf("%w: unconstructed money value", ErrInvalidWrite)
	}
	return numericFrom(m.Amount()), string(m.Commodity()), nil
}

// moneyTo rebuilds a Money from the pair. The two columns are only ever read
// together, which is the point of storing them together.
func moneyTo(amount pgtype.Numeric, commodity string) (ledger.Money, error) {
	value, err := numericTo(amount)
	if err != nil {
		return ledger.Money{}, err
	}
	m, err := ledger.NewMoney(ledger.CommodityCode(commodity), value)
	if err != nil {
		return ledger.Money{}, fmt.Errorf("%w: %w", ErrCorrupt, err)
	}
	return m, nil
}

// rateColumns holds an exchange rate as it sits in a row: four columns that
// are all present or all absent.
type rateColumns struct {
	base  *string
	quote *string
	num   pgtype.Numeric
	den   pgtype.Numeric
}

// rateFrom splits a Rate into its columns, in lowest terms.
//
// big.Rat normalises on construction, so Num and Denom are already reduced and
// the denominator is already positive. The database checks both again, because
// a stored rate that is equal to another but spelled differently would compare
// unequal as a row.
func rateFrom(r ledger.Rate) rateColumns {
	if r.IsZero() {
		return rateColumns{}
	}
	base, quote := string(r.Base()), string(r.Quote())
	value := r.Value()
	return rateColumns{
		base:  &base,
		quote: &quote,
		num:   numericFrom(value.Num()),
		den:   numericFrom(value.Denom()),
	}
}

// rateTo rebuilds a Rate, or the zero Rate when the posting needed none.
func rateTo(c rateColumns) (ledger.Rate, error) {
	if c.base == nil && c.quote == nil && !c.num.Valid && !c.den.Valid {
		return ledger.Rate{}, nil
	}
	if c.base == nil || c.quote == nil || !c.num.Valid || !c.den.Valid {
		return ledger.Rate{}, fmt.Errorf("%w: rate is only partly present", ErrCorrupt)
	}

	num, err := numericTo(c.num)
	if err != nil {
		return ledger.Rate{}, fmt.Errorf("rate numerator: %w", err)
	}
	den, err := numericTo(c.den)
	if err != nil {
		return ledger.Rate{}, fmt.Errorf("rate denominator: %w", err)
	}
	if den.Sign() == 0 {
		return ledger.Rate{}, fmt.Errorf("%w: rate denominator is zero", ErrCorrupt)
	}

	r, err := ledger.NewRate(
		ledger.CommodityCode(*c.base),
		ledger.CommodityCode(*c.quote),
		new(big.Rat).SetFrac(num, den),
	)
	if err != nil {
		return ledger.Rate{}, fmt.Errorf("%w: %w", ErrCorrupt, err)
	}
	return r, nil
}

// dateFrom writes a civil date into a date column.
//
// UTC is not a timezone claim. A civil date has no zone, and date columns
// carry none; UTC is simply the zone that adds nothing when the day is read
// back out.
func dateFrom(d ledger.Date) pgtype.Date {
	if d.IsZero() {
		return pgtype.Date{}
	}
	return pgtype.Date{
		Time:  time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, time.UTC),
		Valid: true,
	}
}

// dateTo reads a date column back as a civil date.
func dateTo(d pgtype.Date) (ledger.Date, error) {
	if !d.Valid {
		return ledger.Date{}, fmt.Errorf("%w: date is null", ErrCorrupt)
	}
	if d.InfinityModifier != pgtype.Finite {
		return ledger.Date{}, fmt.Errorf("%w: date is infinite", ErrCorrupt)
	}
	out, err := ledger.NewDate(d.Time.Year(), d.Time.Month(), d.Time.Day())
	if err != nil {
		return ledger.Date{}, fmt.Errorf("%w: %w", ErrCorrupt, err)
	}
	return out, nil
}

// timestampFrom writes an optional instant. The zero time means the caller
// never knew one, which is different from knowing it was the epoch.
func timestampFrom(t time.Time) pgtype.Timestamptz {
	if t.IsZero() {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: t, Valid: true}
}

// timestampTo reads it back, returning the zero time for NULL.
func timestampTo(t pgtype.Timestamptz) time.Time {
	if !t.Valid {
		return time.Time{}
	}
	return t.Time
}

// textFrom writes an optional string, mapping empty to NULL.
func textFrom(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// textTo reads it back, mapping NULL to empty.
func textTo(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
