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
// # The three types money passes through
//
// Money is an exact whole number of a commodity's smallest unit. It is what
// gets stored, sent and displayed, and it is closed under addition,
// subtraction and multiplication by a whole number.
//
// Rat is an exact fraction of that same smallest unit. Anything that cannot
// stay whole — a share of a total, a division, an exchange rate applied —
// produces one. It cannot be stored or sent anywhere.
//
// Rat.Round is the only way back, and the only rounding in Nusa. That is what
// turns "round once, at the last step" from a habit into something the
// signatures insist on: a calculation that rounds halfway through has to say
// so out loud, where a reviewer can see it.
//
// # What is immutable, and how
//
// Money, Rat, Rate, Posting, Transaction, Account and Lot all keep their
// fields unexported and copy any pointer they hold on the way in and on the
// way out. Nothing a caller receives can be used to change what it came from.
// Lot.Consume returns a reduced lot rather than reducing the receiver, and
// Journal.Add returns a new journal.
//
// # What this package will not do for you
//
// It never invents an identity, and it never reads a clock. Account,
// transaction, posting and lot identities all arrive from outside, which is
// what lets a replayed write be recognised rather than duplicated. Dates
// arrive from outside for the same reason: a domain that can ask what day it
// is has a different answer on every run.
//
// It never balances a transaction on your behalf. Cross-commodity
// transactions balance through conversion postings built explicitly against
// an equity trading account — see AccountTree.ConversionPostings — because a
// validator that quietly closes its own gaps cannot tell a genuine conversion
// from an importer dropping a line.
package ledger
