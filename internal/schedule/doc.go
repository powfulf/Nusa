// SPDX-License-Identifier: AGPL-3.0-only

// Package schedule expands recurring transactions into concrete occurrences.
//
// It holds an RRULE-like engine covering weekly, monthly on day N, "third
// Wednesday", quarterly, end-of-month and yearly patterns, together with
// weekend handling, skipped occurrences and detection of bills that should
// have arrived but have not.
//
// Expansion is a pure function of a rule and a date range, so the same rule
// always produces the same dates.
//
// Implemented in milestone M5.
package schedule
