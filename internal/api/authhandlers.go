// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/GaffaQ/Nusa/internal/auth"
)

// The authentication endpoints.
//
// Two rules govern nearly every decision in this file, and both are about what
// a response is allowed to reveal.
//
// **A refusal says as little as it can.** No such account, wrong password,
// wrong one-time code and spent backup code all answer with one code and one
// status. Registration closed and address already taken likewise. Every
// distinction preserved in the response is an oracle somebody can query
// without an account.
//
// **A refusal costs as much as an acceptance.** Wording alone is not enough:
// an early return on the no-such-account path answers in microseconds while a
// real account costs a full Argon2id verification, and that difference is
// readable with a stopwatch from the other side of the internet. Hence
// VerifyDecoy, and hence the deliberate absence of an audit write on the
// failure path — see auth.Event.

const maxAuthBodyBytes = 4 << 10

type registerRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type secondFactorRequest struct {
	Code string `json:"code"`
}

type sessionResponse struct {
	// Status is "authenticated" or "second_factor_required". A client reads
	// this rather than inferring from the presence of a cookie, because the
	// cookie is set in both cases.
	Status string `json:"status"`

	UserID    string `json:"user_id"`
	Email     string `json:"email"`
	ExpiresAt string `json:"expires_at"`
}

const (
	statusAuthenticated      = "authenticated"
	statusSecondFactorNeeded = "second_factor_required"
)

// decodeBody reads a JSON request body, bounded.
func decodeBody(w http.ResponseWriter, d Deps, r *http.Request, into any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxAuthBodyBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(into); err != nil {
		writeError(w, d.Logger, http.StatusBadRequest, CodeInvalidRequest, "unreadable request body")
		return false
	}
	return true
}

// handleRegister creates the instance's first account.
//
// Whether registration is open is never asked before attempting it. The
// database settles that through users_at_most_one_credentialed, which is what
// makes two simultaneous registrations produce one account; asking first would
// be a check that races and a second code path that leaks the answer.
func handleRegister(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req registerRequest
		if !decodeBody(w, d, r, &req) {
			return
		}

		email := strings.TrimSpace(req.Email)
		if email == "" || !strings.Contains(email, "@") {
			writeErrorDetail(w, d.Logger, http.StatusBadRequest, errorDetail{
				Code: CodeInvalidRequest, Message: "an email address is required", Field: "email",
			})
			return
		}
		if len(req.Password) < minPasswordLength {
			writeErrorDetail(w, d.Logger, http.StatusBadRequest, errorDetail{
				Code:    CodeInvalidRequest,
				Message: "password is too short",
				Field:   "password",
				Details: map[string]string{"min_length": strconv.Itoa(minPasswordLength)},
			})
			return
		}

		hash, err := d.Auth.Hasher.Hash(req.Password)
		if err != nil {
			writeInternalError(w, d.Logger, "hash password", err)
			return
		}

		userID, err := d.Auth.NewID()
		if err != nil {
			writeInternalError(w, d.Logger, "mint user id", err)
			return
		}

		now := d.Auth.now()
		err = d.Auth.Credentials.Register(r.Context(), auth.Registration{
			UserID: userID, Email: email, PasswordHash: hash, At: now,
		})
		switch {
		case errors.Is(err, auth.ErrRegistrationClosed), errors.Is(err, auth.ErrEmailTaken):
			// One answer for both. "Registration is closed" tells an anonymous
			// caller that somebody is using this instance; "that address is
			// taken" tells them who. The operator's log keeps the difference.
			d.Logger.InfoContext(r.Context(), "registration refused",
				slog.String("reason", err.Error()),
				slog.String("client_ip", clientIPString(r, d)))
			writeError(w, d.Logger, http.StatusConflict, CodeRegistrationUnavailable,
				"registration is not available")
			return
		case err != nil:
			writeInternalError(w, d.Logger, "register user", err)
			return
		}

		d.Logger.InfoContext(r.Context(), "account registered",
			slog.String("user_id", userID),
			slog.String("client_ip", clientIPString(r, d)))
		recordEvent(r, d, userID, "account_registered", "user", userID)

		// Registering does not sign anybody in. The password has not been
		// presented for verification yet — it was chosen — and a flow that
		// hands out a session on registration is one where the credential is
		// never actually tested before it grants access.
		writeJSON(w, d.Logger, http.StatusCreated, map[string]string{"user_id": userID})
	}
}

// minPasswordLength is the only password policy here.
//
// Twelve characters, and no composition rules. A required digit and a required
// capital move people towards Password1! and away from four ordinary words,
// which is the wrong direction: length is what carries entropy in the
// distribution people actually draw from. Argon2id at the measured cost is
// what makes even a modest choice expensive to attack.
const minPasswordLength = 12

// handleLogin verifies a password and starts a session.
func handleLogin(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		clientIP := clientIPString(r, d)
		now := d.Auth.now()

		// Checked before anything expensive. A blocked address must not be
		// able to make the server spend 19 MiB and 28 ms per attempt, which is
		// the denial of service the limiter exists to prevent as much as it
		// exists to slow guessing.
		if retry, ok := d.Auth.Limiter.Allow(clientIP, now); !ok {
			w.Header().Set("Retry-After", strconv.Itoa(int(retry.Round(time.Second).Seconds())))
			d.Logger.InfoContext(r.Context(), "sign-in rate limited",
				slog.String("client_ip", clientIP), slog.Duration("retry_after", retry))
			writeErrorDetail(w, d.Logger, http.StatusTooManyRequests, errorDetail{
				Code:    CodeRateLimited,
				Message: "too many failed sign-in attempts",
				Details: map[string]string{"retry_after_seconds": strconv.Itoa(int(retry.Round(time.Second).Seconds()))},
			})
			return
		}

		var req loginRequest
		if !decodeBody(w, d, r, &req) {
			return
		}

		credentials, err := d.Auth.Credentials.CredentialsByEmail(r.Context(), strings.TrimSpace(req.Email))
		switch {
		case errors.Is(err, auth.ErrNoSuchUser):
			// The decoy is not optional and not decoration. Returning here
			// without it answers in microseconds while a real address costs a
			// full verification, and that gap is a reliable account
			// enumeration oracle no matter how carefully this response is
			// worded.
			d.Auth.Hasher.VerifyDecoy()
			d.Auth.Limiter.Fail(clientIP, now)
			logFailedSignIn(r, d, clientIP, false)
			writeInvalidCredentials(w, d)
			return
		case err != nil:
			writeInternalError(w, d.Logger, "look up credentials", err)
			return
		}

		if err := auth.Verify(credentials.PasswordHash, req.Password); err != nil {
			if !errors.Is(err, auth.ErrPasswordMismatch) {
				// An unreadable stored hash is an operational fault, not a
				// failed sign-in. It must not be reported to the person as
				// though they had mistyped, and it must not be hidden from the
				// operator.
				writeInternalError(w, d.Logger, "verify password", err)
				return
			}
			d.Auth.Limiter.Fail(clientIP, now)
			logFailedSignIn(r, d, clientIP, true)
			writeInvalidCredentials(w, d)
			return
		}

		d.Auth.Limiter.Succeed(clientIP)
		issueSession(w, r, d, credentials, now)
	}
}

// writeInvalidCredentials is the single answer every credential failure gets.
//
// One function rather than several call sites so that the status, the code and
// the message cannot drift apart between the no-such-account path and the
// wrong-password path. They have drifted apart in other people's code exactly
// this way: a refactor changes one branch, and the difference becomes the
// oracle.
func writeInvalidCredentials(w http.ResponseWriter, d Deps) {
	writeError(w, d.Logger, http.StatusUnauthorized, CodeInvalidCredentials,
		"email or password is incorrect")
}

// logFailedSignIn records an attempt for an operator.
//
// Both paths call this with the same fields and the same shape, so it adds no
// timing difference between them. accountExists goes to the log and never to
// the response: an operator investigating needs it, and an attacker must not
// have it.
func logFailedSignIn(r *http.Request, d Deps, clientIP string, accountExists bool) {
	d.Logger.InfoContext(r.Context(), "sign-in failed",
		slog.String("client_ip", clientIP),
		slog.Bool("account_exists", accountExists))
}

// issueSession creates the session a successful password step earns.
func issueSession(
	w http.ResponseWriter, r *http.Request, d Deps, credentials auth.Credentials, now time.Time,
) {
	enrolment, err := d.Auth.SecondFactors.TOTPEnrolment(r.Context(), credentials.UserID)
	switch {
	case errors.Is(err, auth.ErrNoTOTPEnrolment):
		// No second factor configured. The session is authenticated outright.
	case err != nil:
		writeInternalError(w, d.Logger, "read second factor", err)
		return
	}
	// An enrolment that was started and never proved is not a second factor.
	// Treating it as one would lock somebody out with a QR code they closed.
	authenticated := !enrolment.Confirmed()

	sessionID, err := d.Auth.NewID()
	if err != nil {
		writeInternalError(w, d.Logger, "mint session id", err)
		return
	}
	token, err := auth.NewSessionToken()
	if err != nil {
		writeInternalError(w, d.Logger, "draw session token", err)
		return
	}

	expires := now.Add(d.Auth.SessionTTL)
	err = d.Auth.Sessions.CreateSession(r.Context(), auth.NewSession{
		ID: sessionID, Token: token, UserID: credentials.UserID,
		CreatedAt: now, ExpiresAt: expires, Authenticated: authenticated,
	})
	if err != nil {
		writeInternalError(w, d.Logger, "create session", err)
		return
	}

	setSessionCookie(w, token, expires, d.Auth.SecureCookies)

	status := statusSecondFactorNeeded
	action := "sign_in_pending_second_factor"
	if authenticated {
		status = statusAuthenticated
		action = "sign_in"
	}

	d.Logger.InfoContext(r.Context(), "sign-in succeeded",
		slog.String("user_id", credentials.UserID),
		slog.String("session_id", sessionID),
		slog.String("client_ip", clientIPString(r, d)),
		slog.Bool("second_factor_required", !authenticated))
	recordEvent(r, d, credentials.UserID, action, "session", sessionID)

	writeJSON(w, d.Logger, http.StatusOK, sessionResponse{
		Status: status, UserID: credentials.UserID, Email: credentials.Email,
		ExpiresAt: expires.Format(time.RFC3339),
	})
}

// handleSecondFactor completes a sign-in that is waiting on a code.
//
// It accepts either a TOTP code or a backup code, and does not ask which. A
// six-digit code and a sixteen-character one are told apart by trying, so a
// client does not have to know the difference and a person does not have to
// choose a mode before typing.
func handleSecondFactor(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		session, ok := sessionFrom(r.Context())
		if !ok {
			writeInternalError(w, d.Logger, "session missing from context",
				errors.New("handleSecondFactor used outside requireSession"))
			return
		}
		if !session.PendingSecondFactor() {
			writeError(w, d.Logger, http.StatusConflict, CodeInvalidRequest,
				"this session has already completed its second factor")
			return
		}

		var req secondFactorRequest
		if !decodeBody(w, d, r, &req) {
			return
		}

		clientIP := clientIPString(r, d)
		now := d.Auth.now()
		if retry, allowed := d.Auth.Limiter.Allow(clientIP, now); !allowed {
			w.Header().Set("Retry-After", strconv.Itoa(int(retry.Round(time.Second).Seconds())))
			writeErrorDetail(w, d.Logger, http.StatusTooManyRequests, errorDetail{
				Code: CodeRateLimited, Message: "too many failed attempts",
			})
			return
		}

		code := strings.TrimSpace(req.Code)
		used := "totp"
		err := d.Auth.SecondFactors.ConsumeTOTPCode(
			r.Context(), d.Auth.Authenticator, session.UserID, code, now)
		if err != nil {
			// A one-time code that does not match is tried as a backup code
			// before the attempt is refused, so that a person who has lost
			// their phone is not asked to choose a different form first.
			if backupErr := d.Auth.SecondFactors.ConsumeBackupCode(
				r.Context(), session.UserID, code, now); backupErr != nil {
				d.Auth.Limiter.Fail(clientIP, now)
				d.Logger.InfoContext(r.Context(), "second factor failed",
					slog.String("user_id", session.UserID),
					slog.String("client_ip", clientIP),
					slog.String("totp_error", err.Error()),
					slog.String("backup_error", backupErr.Error()))
				writeError(w, d.Logger, http.StatusUnauthorized, CodeInvalidCredentials,
					"that code is not valid")
				return
			}
			used = "backup_code"
		}

		newToken, err := auth.NewSessionToken()
		if err != nil {
			writeInternalError(w, d.Logger, "draw session token", err)
			return
		}
		if err := d.Auth.Sessions.ElevateSession(r.Context(), session.ID, newToken, now); err != nil {
			if errors.Is(err, auth.ErrSessionNotUsable) {
				writeError(w, d.Logger, http.StatusUnauthorized, CodeUnauthenticated,
					"this session can no longer be completed")
				return
			}
			writeInternalError(w, d.Logger, "elevate session", err)
			return
		}

		d.Auth.Limiter.Succeed(clientIP)
		setSessionCookie(w, newToken, session.ExpiresAt, d.Auth.SecureCookies)

		d.Logger.InfoContext(r.Context(), "second factor verified",
			slog.String("user_id", session.UserID),
			slog.String("session_id", session.ID),
			slog.String("method", used))
		recordEvent(r, d, session.UserID, "second_factor_verified", "session", session.ID)

		writeJSON(w, d.Logger, http.StatusOK, sessionResponse{
			Status: statusAuthenticated, UserID: session.UserID,
			ExpiresAt: session.ExpiresAt.Format(time.RFC3339),
		})
	}
}

// handleLogout ends a session.
//
// It revokes first and clears the cookie second, and the order is not
// cosmetic. Clearing a cookie deletes one copy of a credential from one
// browser; it does nothing about a token that has already been read from that
// browser by something else. Revocation is what ends the session, and a
// sign-out that only cleared the cookie would look identical to the person and
// be worth nothing against the case they were signing out *because of*.
//
// A session awaiting its second factor may sign out. It holds a live cookie,
// and being unable to withdraw a live credential is a worse position than
// being able to.
func handleLogout(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		session, ok := sessionFrom(r.Context())
		if !ok {
			writeInternalError(w, d.Logger, "session missing from context",
				errors.New("handleLogout used outside requireSession"))
			return
		}

		now := d.Auth.now()
		if err := d.Auth.Sessions.RevokeSession(r.Context(), session.ID, now); err != nil &&
			!errors.Is(err, auth.ErrNoSession) {
			// The cookie is deliberately not cleared on a failure. Telling the
			// browser the session is gone when the server still accepts the
			// token would leave somebody believing they had signed out.
			writeInternalError(w, d.Logger, "revoke session", err)
			return
		}

		clearSessionCookie(w, d.Auth.SecureCookies)
		d.Logger.InfoContext(r.Context(), "signed out",
			slog.String("user_id", session.UserID), slog.String("session_id", session.ID))
		recordEvent(r, d, session.UserID, "sign_out", "session", session.ID)

		w.WriteHeader(http.StatusNoContent)
	}
}

// handleCurrentSession answers who the caller is. It exists as the smallest
// possible authenticated endpoint, which is what a pending session must not be
// able to reach.
func handleCurrentSession(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		session, ok := sessionFrom(r.Context())
		if !ok {
			writeInternalError(w, d.Logger, "session missing from context",
				errors.New("handleCurrentSession used outside requireSession"))
			return
		}
		writeJSON(w, d.Logger, http.StatusOK, sessionResponse{
			Status: statusAuthenticated, UserID: session.UserID,
			ExpiresAt: session.ExpiresAt.Format(time.RFC3339),
		})
	}
}

// recordEvent appends an authentication event, and never fails a request.
//
// A sign-in that succeeded has succeeded; refusing it because the audit write
// failed would turn a logging fault into an outage. The failure is logged at
// error level, which is where an operator finds it.
func recordEvent(r *http.Request, d Deps, actorID, action, entityKind, entityID string) {
	id, err := d.Auth.NewID()
	if err != nil {
		d.Logger.ErrorContext(r.Context(), "mint audit id", slog.String("error", err.Error()))
		return
	}
	event := auth.Event{
		ID: id, ActorID: actorID, At: d.Auth.now(),
		Action: action, EntityKind: entityKind, EntityID: entityID,
	}
	if err := d.Auth.Events.RecordEvent(r.Context(), event); err != nil {
		d.Logger.ErrorContext(r.Context(), "record auth event",
			slog.String("action", action), slog.String("error", err.Error()))
	}
}
