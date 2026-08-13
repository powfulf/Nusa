// SPDX-License-Identifier: AGPL-3.0-only

// Package scenario provides branchable what-if projections: hypothetical
// events layered on top of real data without ever touching the real ledger.
//
// A scenario is a branch of a book. It can be compared against another
// scenario and merged into reality if the events actually happen. Every
// assumption a projection relies on must be visible and editable — this is a
// calculator, not a forecast, and it never hides a magic number.
//
// Implemented in milestone M10.
package scenario
