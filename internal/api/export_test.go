// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"net/http"

	"github.com/GaffaQ/Nusa/internal/ledger"
)

// Test-only handles on unexported middleware.
//
// The middleware is exercised directly rather than only through the router
// because one property cannot be reached from a route: what happens when a
// session is revoked while a handler is already running. Proving that needs a
// handler the test can hold open, and the routes are all real endpoints.
var (
	RequireSessionForTest        func(Deps) func(http.Handler) http.Handler = requireSession
	RequireAuthenticatedForTest  func(Deps) func(http.Handler) http.Handler = requireAuthenticated
	RequireIdempotencyKeyForTest func(Deps) func(http.Handler) http.Handler = requireIdempotencyKey
)

// The mutation response path, exercised behind a test-only route.
//
// No ledger endpoint exists yet — they arrive later in this phase — and
// waiting for one would mean writing the replay logic now and proving it
// later, which is the order that produces a guard nobody has watched fail.
var (
	WriteIdempotentResponseForTest  = writeIdempotentResponse
	WriteIdempotencyConflictForTest = writeIdempotencyConflict
	IdempotencyKeyHeaderForTest     = idempotencyKeyHeader
	MaxIdempotencyKeyLengthForTest  = maxIdempotencyKeyLength
)

// The cursor codec. It has no route in front of it yet — the transaction
// endpoints arrive later in this phase — and the encoding is exactly the kind
// of thing that is cheap to prove now and expensive to unpick once a client
// holds tokens produced by it.
var (
	EncodeCursorForTest           = encodeCursor
	DecodeCursorForTest           = decodeCursor
	ErrCursorFilterChangedForTest = errCursorFilterChanged
)

// TransactionFiltersForTest builds the filter set a cursor is bound to.
func TransactionFiltersForTest(from, to ledger.Date) transactionFilters {
	return transactionFilters{From: from, To: to}
}

// DigestForTest exposes the filter fingerprint so a test can build a token
// with a correct digest and a deliberately wrong field elsewhere.
func DigestForTest(f transactionFilters) string { return f.digest() }

// SecondFactorRoutesForTest is the table the enumerating guard walks. Exposing
// it rather than repeating it in the test is the point: a route added to the
// router and not to the table is a route the guard reports, and a route added
// to both is covered without anybody writing a test for it.
var SecondFactorRoutesForTest = secondFactorRoutes

// The OpenAPI document and the code inventory, for the guards that compare
// them with the router and with the constant block.
var (
	OpenAPIDocumentForTest = openAPIDocument
	AllErrorCodesForTest   = allErrorCodes
)
