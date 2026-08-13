// SPDX-License-Identifier: AGPL-3.0-only

// Package i18n holds server-side message catalogs and locale resolution.
//
// The frontend owns most user-facing text; what lives here is what the server
// must produce itself, such as error messages and exported documents. The API
// prefers returning a stable error code and letting the client translate it,
// so this package stays small on purpose.
//
// Locale governs number format, date format, first day of week and currency
// display independently of language.
//
// Implemented in milestone M2.
package i18n
