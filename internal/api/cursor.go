// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/powfulf/Nusa/internal/ledger"
)

// The opaque cursor a paginated endpoint hands back.
//
// It carries three things: a version, a hash of the filters the page was
// produced under, and the position itself. The position is what the store
// needs; the other two exist to refuse a request that cannot be answered
// honestly rather than to answer a different question quietly.
//
// It is deliberately NOT SIGNED, and the reason is narrow enough that it must
// be read before being reused. Everything a cursor names — a date and a
// transaction identity — is already readable by the session presenting it,
// because every ledger endpoint is authorised by holding a session and by
// nothing narrower. Forging a position therefore grants nothing that asking
// politely would not, and a signature would protect a secret that is not one.
//
// That is a fact about *this* resource under *that* authorisation decision,
// not a general claim that cursors need no integrity. A cursor encoding a
// filter the server would otherwise impose — a household, an owner, a
// visibility scope — would be carrying a capability, and forging it would be
// privilege escalation. When M9 adds households, this reasoning is one of the
// things that has to be re-examined, not inherited.

// cursorVersion prefixes every encoded cursor.
//
// A cursor outlives the code that made it: a client can hold one across a
// deployment and present it afterwards. When the encoding changes, the version
// is what lets the new code say "this is from before" instead of decoding
// old bytes under new rules and landing somewhere plausible and wrong.
const cursorVersion = "v1"

// cursorFilterDigestLength is how much of the filter hash travels.
//
// Eight hex characters. This is a change-detector, not a security boundary —
// it exists so that a client which alters a filter mid-walk is told, rather
// than being handed a page that answers a different question than the one
// before it. A collision means one such mistake goes unreported; it does not
// let anyone read anything.
const cursorFilterDigestLength = 8

// errCursorFilterChanged reports a cursor presented alongside different
// filters from the ones it was produced under.
var errCursorFilterChanged = errors.New("cursor was issued for a different filter set")

// transactionFilters are the query parameters a page was produced under.
//
// Only the fields that change *which* rows are returned belong here. The page
// size does not: asking for the next twenty rows instead of the next fifty is
// still the same question, continued, and refusing it would be pedantry a
// caller cannot act on.
type transactionFilters struct {
	From ledger.Date
	To   ledger.Date
}

// digest fingerprints the filter set.
//
// Built from the canonical string form of each field rather than from whatever
// the caller typed, so "2026-3-1" and "2026-03-01" — which name the same day —
// do not read as a changed filter.
//
// The length prefix stops two adjacent fields hashing the same as one longer
// one. It cannot be falsified by any test here, and that is worth saying
// rather than leaving for someone to discover by deleting it: both fields
// today are either empty or exactly ten characters, so no two filter sets can
// concatenate alike. It stops being decorative the moment a filter of variable
// length is added — an account name, a payee, a free-text search — and adding
// one of those is not the moment to remember this.
func (f transactionFilters) digest() string {
	h := sha256.New()
	for _, part := range []string{f.From.String(), f.To.String()} {
		// hash.Hash never returns an error from Write, which is why the
		// package documents it and why the rest of this repository writes to a
		// digest the same way.
		_, _ = io.WriteString(h, strconv.Itoa(len(part)))
		_, _ = io.WriteString(h, ":")
		_, _ = io.WriteString(h, part)
	}
	return fmt.Sprintf("%x", h.Sum(nil))[:cursorFilterDigestLength]
}

// encodeCursor renders a position as an opaque token.
func encodeCursor(f transactionFilters, date ledger.Date, id ledger.TransactionID) string {
	raw := strings.Join([]string{cursorVersion, f.digest(), date.String(), string(id)}, "\x1f")
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

// decodeCursor reads a token back, refusing anything it cannot answer exactly.
//
// Every rejection here is a case where the alternative is to guess: an unknown
// version means the fields no longer mean what they say, and a changed filter
// means the position was computed over a different set of rows. Continuing
// from a position established under other filters produces a page that looks
// ordinary and silently skips whatever the two filter sets disagree about.
func decodeCursor(f transactionFilters, token string) (ledger.Date, ledger.TransactionID, error) {
	decoded, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return ledger.Date{}, "", fmt.Errorf("cursor is not base64url: %w", err)
	}

	parts := strings.Split(string(decoded), "\x1f")
	if len(parts) != 4 {
		return ledger.Date{}, "", fmt.Errorf("cursor has %d fields, not 4", len(parts))
	}
	if parts[0] != cursorVersion {
		return ledger.Date{}, "", fmt.Errorf("cursor version %q is not %q", parts[0], cursorVersion)
	}
	if parts[1] != f.digest() {
		return ledger.Date{}, "", errCursorFilterChanged
	}

	date, err := ledger.ParseDate(parts[2])
	if err != nil {
		return ledger.Date{}, "", fmt.Errorf("cursor date: %w", err)
	}
	if err := ledger.ValidateID(parts[3]); err != nil {
		return ledger.Date{}, "", fmt.Errorf("cursor id: %w", err)
	}
	return date, ledger.TransactionID(parts[3]), nil
}
