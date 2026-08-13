// SPDX-License-Identifier: AGPL-3.0-only

// Package auth handles registration, login, sessions and two-factor
// authentication.
//
// Passwords are hashed with Argon2id. Sessions are server-side and revocable,
// carried in HttpOnly, Secure, SameSite=Lax cookies — there are no JWTs.
// Second factor is TOTP with backup codes. Login endpoints are rate limited.
//
// Implemented in milestone M2.
package auth
