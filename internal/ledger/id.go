// SPDX-License-Identifier: MIT

package ledger

import (
	"fmt"
	"strings"
)

// AccountID, TransactionID, PostingID and LotID are separate types so that one
// can never be passed where another was meant. They share this file because
// they share a shape: a canonical, lowercase UUIDv7, supplied from outside.
//
// This package never generates one. §5.6 puts that on the client, and it is
// what makes a replayed write recognisable rather than duplicated — the second
// attempt carries the identity the first one did, so the store can tell it is
// the same write rather than a new one.
//
// UUIDv7 specifically, rather than any unique string, because it is generated
// without coordination and sorts by creation time, which keeps the primary-key
// index M2 will build on it from fragmenting.

const (
	uuidLength      = 36
	uuidVersionPos  = 14 // the version nibble, "7" in a v7
	uuidVariantPos  = 19 // the variant nibble, one of 8, 9, a, b
	uuidVersionSpec = '7'
)

var uuidHyphenPositions = [4]int{8, 13, 18, 23}

// ValidateID reports whether an identity is a canonical UUIDv7.
//
// It is exported so the layers above can reject a malformed identity at the
// edge, where the error can be turned into a useful response, rather than
// letting it travel inward and fail here.
//
// Only lowercase is accepted. A UUID that could arrive in two spellings could
// arrive twice, and the uniqueness this package enforces is by exact string
// comparison — accepting both cases would make two spellings of one identity
// look like two identities, which is the collision the check exists to stop.
func ValidateID(id string) error {
	if id == "" {
		return fmt.Errorf("%w: empty", ErrInvalidID)
	}
	if len(id) != uuidLength {
		return fmt.Errorf("%w: %q is %d characters, a UUID is %d", ErrInvalidID, id, len(id), uuidLength)
	}
	for _, pos := range uuidHyphenPositions {
		if id[pos] != '-' {
			return fmt.Errorf("%w: %q is not in 8-4-4-4-12 form", ErrInvalidID, id)
		}
	}
	for i := 0; i < len(id); i++ {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			continue
		}
		if !isLowerHex(id[i]) {
			return fmt.Errorf("%w: %q contains %q, expected lowercase hexadecimal",
				ErrInvalidID, id, string(id[i]))
		}
	}
	if id[uuidVersionPos] != uuidVersionSpec {
		return fmt.Errorf("%w: %q is a UUID version %s, and identities here are version 7",
			ErrInvalidID, id, string(id[uuidVersionPos]))
	}
	if !strings.ContainsRune("89ab", rune(id[uuidVariantPos])) {
		return fmt.Errorf("%w: %q does not carry the RFC 9562 variant bits", ErrInvalidID, id)
	}
	return nil
}

func isLowerHex(c byte) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')
}

// validateIDAs wraps ValidateID with the sentinel for the entity being built,
// so a caller can match on either "this account is wrong" or "this identity is
// malformed" without parsing a message.
func validateIDAs(kind error, what, id string) error {
	if err := ValidateID(id); err != nil {
		return fmt.Errorf("%w: %s: %w", kind, what, err)
	}
	return nil
}
