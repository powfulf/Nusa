// SPDX-License-Identifier: AGPL-3.0-only

// Package countrypack loads and runs Country Pack plugins. Everything
// country-specific lives in a pack; none of it belongs in core.
//
// A pack is a directory with a manifest describing categories, institutions,
// importers, price and data providers, goal templates, holidays and retirement
// schemes. Most of it is declarative YAML validated against a schema, with
// Starlark as an escape hatch for parsing that YAML cannot express.
//
// The Starlark sandbox gets no network, no filesystem, a deterministic
// injected clock, and bounded execution steps and memory. Packs are loaded and
// validated at startup, and a pack that fails validation is reported clearly
// rather than silently ignored.
//
// The Country Pack specification is MIT licensed so packs can be written and
// shared freely.
//
// Implemented in milestone M8.
package countrypack
