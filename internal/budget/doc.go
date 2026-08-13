// SPDX-License-Identifier: AGPL-3.0-only

// Package budget implements envelope budgeting: allocations per period,
// rollover policy, savings jars and goals.
//
// Budgets are a virtual layer over the ledger. Allocating money to an envelope
// never creates a posting and never moves money between accounts. That
// separation is what allows both correct double-entry accounting and envelope
// budgeting that behaves the way people expect.
//
// Implemented in milestone M5.
package budget
