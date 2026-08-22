// SPDX-License-Identifier: MIT

package ledger

import (
	"fmt"
	"strings"
)

// Text a ledger can hold.
//
// Every string a user writes into the book — a payee, a memo, an account name
// — is stored verbatim. It is user data, not a UI string, so nothing here
// trims it, normalises it, case-folds it or transliterates it. A payee is
// whatever the user typed, because the point of the field is that it matches
// what is printed on the statement.
//
// One code point is refused: U+0000. It is not an aesthetic objection. NUL
// terminates a C string, cannot appear in a PostgreSQL text value, is invalid
// in JSON, is refused in filenames and in HTTP header values, and truncates
// anything that reaches those boundaries through a C API. A ledger entry has
// to survive being written down, sent over a wire, exported to a file and read
// back, and a value carrying NUL does not survive any of those intact.
//
// So it is refused here, in the domain, rather than at whichever edge notices
// first. The store found it first — a generated payee containing U+0000 failed
// a write with SQLSTATE 22021, an error naming neither the field nor the fix —
// and the store still refuses it, deliberately and redundantly, in the way the
// deferred balance trigger redundantly refuses an unbalanced transaction. But
// "text a ledger can hold" is a fact about the ledger, not about PostgreSQL,
// and a rule enforced only by the current storage engine is a rule that a
// future importer, exporter or rule engine writes around without noticing.
//
// It is refused, never stripped. Quietly dropping a character out of what
// someone typed is how a payee stops matching the bank statement it was copied
// from, and no error is ever raised to say so.
func validateText(what, value string) error {
	if i := strings.IndexByte(value, 0); i >= 0 {
		return fmt.Errorf("%w: %s contains a NUL byte at offset %d", ErrInvalidText, what, i)
	}
	return nil
}
