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

const (
	// Name is the product name, for logs, HTTP headers and machine-readable
	// metadata. Do not render it in the UI — use the i18n catalogs.
	Name = "Nusa"

	// Slug is the lowercase, filesystem- and URL-safe form of Name. It is used
	// for binary names, container names, cookie prefixes and database roles.
	Slug = "nusa"

	// RepositoryURL is the canonical source location, surfaced in health
	// responses and the OpenAPI document so an operator can identify exactly
	// what they are running.
	RepositoryURL = "https://github.com/nusa-app/nusa"
)
