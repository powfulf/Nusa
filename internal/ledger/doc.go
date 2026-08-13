// SPDX-License-Identifier: MIT

// Package ledger is the domain core: Money, Commodity, Account, Transaction,
// Posting and Lot, plus the rules that keep them consistent.
//
// This package is pure domain logic. It must never import database/sql,
// net/http, or any configuration package, and it must never read the wall
// clock — every instant it needs is passed in. If something here appears to
// need one of those, the design is wrong somewhere else.
//
// Everything above this package depends on it; it depends on nothing.
//
// Unlike the rest of the project, this package is MIT licensed, so the
// accounting core can be reused freely. See the LICENSE file in this directory.
//
// Implemented in milestone M1.
package ledger
