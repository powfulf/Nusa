// SPDX-License-Identifier: AGPL-3.0-only

// Package invest builds holdings, cost basis and portfolio views on top of the
// Lot model in the ledger package.
//
// It also defines the PriceProvider interface. Prices are fetched server-side
// and cached per instance, never per user: an instance must not flood
// third-party servers, and must not leak an individual user's request
// patterns. Concrete providers beyond manual entry come from Country Packs.
//
// This package records and computes; it does not advise. Nothing here produces
// a buy or sell recommendation.
//
// Implemented in milestone M7.
package invest
