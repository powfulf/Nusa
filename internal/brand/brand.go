// SPDX-License-Identifier: AGPL-3.0-only

// Package brand holds the product's identity as constants.
//
// The product name is a codename and is not final. This file is the only place
// in Go source that spells it out; message catalogs under web/src/i18n are the
// only other place it appears in shipped code. Renaming the product should be
// a change to this file and those catalogs, and nothing else.
//
// Nothing here is a user-facing string. Anything rendered to a user goes
// through i18n, where a translator can decide whether the name is even
// transliterated.
package brand

// The constants below answer two different questions, and conflating them is
// the mistake this comment exists to prevent.
//
// Name and Slug are the product's identity. They change only if the product is
// renamed, which is a product decision — moving the source somewhere else does
// not touch them.
//
// RepositoryURL is where the source currently lives. It changes whenever the
// repository moves, which is a hosting decision — and renaming the product does
// not touch it.
//
// The two have already drifted apart once: the module path moved to
// github.com/GaffaQ/Nusa while the product stayed Nusa. Expect them to keep
// drifting, and do not "tidy" one to match the other.
const (
	// Name is the product name, for logs, HTTP headers and machine-readable
	// metadata. Do not render it in the UI — use the i18n catalogs.
	Name = "Nusa"

	// Slug is the lowercase, filesystem- and URL-safe form of Name. It is used
	// for binary names, container names, cookie prefixes and database roles.
	Slug = "nusa"

	// RepositoryURL is the canonical source location, surfaced in health
	// responses and the OpenAPI document so an operator can identify exactly
	// what they are running. It tracks the repository, not the product name.
	RepositoryURL = "https://github.com/GaffaQ/Nusa"
)
