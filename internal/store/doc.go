// SPDX-License-Identifier: AGPL-3.0-only

// Package store owns everything that talks to PostgreSQL: the connection pool,
// the sqlc-generated queries, and the repository interfaces the rest of the
// application depends on.
//
// Query code in this package is generated from db/queries by sqlc and must not
// be edited by hand. Run `make generate` after changing a query or a
// migration.
//
// The domain does not know this package exists. Dependencies point inward:
// store may import the domain, never the reverse.
package store
