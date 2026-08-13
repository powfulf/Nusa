// SPDX-License-Identifier: AGPL-3.0-only

// Package household implements books shared by several users, with roles and
// per-account and per-category visibility scopes.
//
// This is the most dangerous area of the application: a design mistake here
// leaks somebody's private spending to their partner or family. Security wins
// over convenience in every decision. Anything newly created defaults to
// private, a transaction inherits the strictest visibility of its account and
// category, and widening visibility requires explicit confirmation naming who
// will be able to see it.
//
// Every cross-user access is recorded in the audit log.
//
// Implemented in milestone M9.
package household
