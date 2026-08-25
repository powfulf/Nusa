// SPDX-License-Identifier: AGPL-3.0-only

package auth

import "errors"

// Every error leaving this package wraps one of these sentinels, so callers
// match on behaviour with errors.Is rather than on message text.
//
// None of these messages is ever shown to a user, and the reason is sharper
// here than elsewhere in Nusa: an authentication error that is precise is an
// authentication error that is useful to an attacker. The HTTP layer collapses
// ErrPasswordMismatch and "no such account" into one indistinguishable
// response. It can only do that if the distinction survives to the point where
// the decision is made, which is why these are separate values down here and
// one response up there.
var (
	// ErrPasswordMismatch reports a password that does not match the hash it
	// was checked against.
	//
	// It says nothing about whether an account exists. The caller is
	// responsible for making sure a missing account produces this same
	// outcome, in the same amount of time — see Hasher.VerifyDecoy.
	ErrPasswordMismatch = errors.New("password does not match")

	// ErrMalformedHash reports an encoded hash that cannot be parsed: wrong
	// field count, unparseable parameters, or invalid base64.
	//
	// This is corruption, not a failed login. A stored credential that cannot
	// be read is an operational problem and must never be reported to the
	// person trying to log in as though they had typed the wrong password.
	ErrMalformedHash = errors.New("malformed password hash")

	// ErrUnsupportedHash reports a well-formed hash this build cannot verify:
	// an algorithm other than argon2id, a different Argon2 version, or cost
	// parameters beyond the ceiling this package is willing to spend.
	ErrUnsupportedHash = errors.New("unsupported password hash")

	// ErrInvalidParameters reports a Hasher configured with costs that are
	// outside what Argon2id accepts, or so low they would be theatre.
	ErrInvalidParameters = errors.New("invalid hashing parameters")

	// ErrInvalidCode reports a one-time code that matches no accepted window.
	ErrInvalidCode = errors.New("invalid one-time code")

	// ErrCodeReused reports a correct one-time code that has already been
	// spent.
	//
	// It is separate from ErrInvalidCode because they are different events to
	// whoever reads the log: a mistyped code is noise, while a valid code
	// arriving a second time is what a replayed credential looks like. The
	// person signing in is told the same thing either way — the distinction is
	// for the operator, not for them.
	ErrCodeReused = errors.New("one-time code already used")

	// ErrInvalidSecret reports a shared secret that is unreadable as base32 or
	// too short to be worth using.
	ErrInvalidSecret = errors.New("invalid totp secret")

	// ErrInvalidAuthenticator reports TOTP parameters that would generate
	// codes nothing can match: an unknown algorithm, a digit count outside
	// what RFC 4226 defines, a fractional period, or an absurd skew.
	ErrInvalidAuthenticator = errors.New("invalid totp parameters")

	// ErrInvalidBackupCode reports a recovery code matching none of the codes
	// a user holds. It does not distinguish "never issued" from "already
	// spent": the caller removes a spent code's hash from the set, so by the
	// time this package sees it the two are the same fact.
	ErrInvalidBackupCode = errors.New("invalid backup code")

	// ErrRegistrationClosed reports a registration refused because the
	// instance already has an account.
	//
	// It is a real distinction at this layer and must not survive to the HTTP
	// response. "Registration is closed" tells an anonymous caller that
	// somebody is using this instance, which is the same shape of leak as
	// account enumeration; the handler answers the same way it answers any
	// refused registration.
	ErrRegistrationClosed = errors.New("registration is closed")

	// ErrEmailTaken reports an address already registered. Unreachable while
	// ErrRegistrationClosed covers every second account, and defined now
	// because M9's invitations make it reachable without changing this layer.
	ErrEmailTaken = errors.New("email is already registered")

	// ErrNoSuchUser reports an account that does not exist.
	//
	// The caller is responsible for making a missing account indistinguishable
	// from a wrong password, in the answer and in the time taken. See
	// Hasher.VerifyDecoy.
	ErrNoSuchUser = errors.New("no such user")

	// ErrNoSession reports a token matching no session at all — a forged,
	// stale or truncated cookie.
	ErrNoSession = errors.New("no such session")

	// ErrSessionNotUsable reports a session that exists and is finished:
	// expired, revoked, or created before the password was last changed.
	//
	// Separate from ErrNoSession because the two mean different things to
	// whoever reads the log — one is a credential that never existed, the
	// other is one that did — while meaning the same thing to the person, who
	// signs in again either way.
	ErrSessionNotUsable = errors.New("session is no longer usable")

	// ErrNoTOTPEnrolment reports a user with no second factor on record.
	ErrNoTOTPEnrolment = errors.New("no totp enrolment")

	// ErrTOTPNotConfirmed reports an enrolment that was started and never
	// proved. An unconfirmed enrolment is not a second factor, and treating it
	// as one would lock somebody out with a secret they never scanned.
	ErrTOTPNotConfirmed = errors.New("totp enrolment is not confirmed")

	// ErrTOTPAlreadyConfirmed reports a second attempt to finish an enrolment
	// that is already finished.
	ErrTOTPAlreadyConfirmed = errors.New("totp enrolment is already confirmed")

	// ErrTimeBeforeEpoch reports an instant before 1970. The TOTP counter is
	// unsigned, so a negative second would wrap rather than fail.
	ErrTimeBeforeEpoch = errors.New("time is before the unix epoch")
)
