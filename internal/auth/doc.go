// SPDX-License-Identifier: AGPL-3.0-only

// Package auth handles registration, login, sessions and two-factor
// authentication.
//
// Passwords are hashed with Argon2id. Sessions are server-side and revocable,
// carried in HttpOnly, Secure, SameSite=Lax cookies — there are no JWTs.
// Second factor is TOTP with backup codes. Login endpoints are rate limited.
//
// # This package is pure
//
// Nothing here opens a database, reads configuration or knows what an HTTP
// request is. Credentials and sessions are rows, and they reach storage through
// repository interfaces that internal/store implements — the same arrangement
// internal/ledger has, enforced the same way, by a depguard rule in
// .golangci.yml rather than by this paragraph.
//
// The reason is narrower than tidiness. The only external evidence that the
// TOTP implementation is correct is RFC 6238's published test vectors, and a
// test that needs a container to run is a test that eventually stops being run.
// Every check in this package executes on a plain `go test`.
//
// # What exists
//
// Argon2id password hashing with the cost parameters and their justification in
// password.go, PHC-format encoding, rehash detection, and a decoy verification
// path so that a missing account takes as long as a wrong password.
//
// TOTP written over crypto/hmac and encoding/base32, checked against the whole
// of RFC 6238 Appendix B, with an explicit and reasoned clock-skew window and a
// returned counter the caller must record to prevent replay.
//
// Single-use backup codes, hashed with SHA-256 rather than Argon2id — the
// reasoning, which turns on their entropy being drawn rather than chosen, is in
// backupcode.go where the question occurs.
//
// # What does not exist yet
//
// Sessions, registration and login flows, TOTP enrolment, rate limiting, and
// every repository interface the above will need. Those arrive with the rest of
// milestone M2b.
package auth
