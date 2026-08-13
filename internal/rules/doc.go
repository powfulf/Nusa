// SPDX-License-Identifier: AGPL-3.0-only

// Package rules is the trigger-to-action automation engine: match a
// transaction on merchant, amount, description, account, date or direction,
// then set a category, add tags, assign an envelope, split it or flag it.
//
// Rules are evaluated in an explicit order, can be dry-run against existing
// transactions before being saved, and record which rule changed what so the
// audit trail stays honest. A rule may never leave the ledger unbalanced.
//
// Merchant normalisation lives here too, so that "TOKOPEDIA*12345" and
// "TOKOPEDIA JKT" resolve to one merchant with aliases.
//
// Implemented in milestone M5.
package rules
