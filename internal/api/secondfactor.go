// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"errors"
	"net/http"

	"github.com/GaffaQ/Nusa/internal/auth"
	"github.com/GaffaQ/Nusa/internal/brand"
)

// Setting up a second factor.
//
// Phase 1 left a person able to be *asked* for a second factor and unable to
// configure one: the store and the domain were complete and no HTTP surface
// reached them. This is that surface.
//
// The rule these routes share is that **changing a second factor requires
// presenting one**, wherever there is one to present. Disabling TOTP and
// reissuing backup codes both accept a current code and refuse without it;
// beginning an enrolment does not, because at that point there is nothing to
// present. Without that, a stolen session could quietly remove the very
// control that was supposed to survive a stolen password — and "it is already
// behind requireAuthenticated" sounds like enough right up until it is not.
//
// The requirement is enforced by a route table rather than by remembering it
// in each handler, and there is a test that walks that table rather than one
// test per endpoint. A route added later that forgets the factor will not have
// a test that forgot to be written for it.

// secondFactorRoutes names every route under /api/v1/auth that changes a
// second factor, and says whether it must be given one.
//
// It is a package-level table so that a guard can enumerate it. A handler that
// checked for itself would be correct and unenumerable, and the next route
// would be correct or not depending on whether somebody remembered.
var secondFactorRoutes = []struct {
	Method string
	Path   string

	// RequiresCode is true where a current one-time or backup code must be
	// presented alongside the session.
	RequiresCode bool
}{
	{http.MethodPost, "/api/v1/auth/totp", false},
	{http.MethodPost, "/api/v1/auth/totp/confirm", false},
	{http.MethodDelete, "/api/v1/auth/totp", true},
	{http.MethodPost, "/api/v1/auth/backup-codes", true},
	{http.MethodGet, "/api/v1/auth/backup-codes", false},
}

type beginEnrolmentResponse struct {
	// Secret is shown exactly once, here. It is never readable again: the
	// column holds it so that codes can be checked, not so that it can be
	// handed back.
	Secret string `json:"secret"`

	// URI is the otpauth:// form the same secret takes for a QR code.
	URI string `json:"uri"`
}

type backupCodesResponse struct {
	// Codes are shown exactly once, when they are issued. There is no
	// endpoint that reads them back, because a list of backup codes readable
	// from a live session is a second password that never expires.
	Codes []string `json:"backup_codes"`
}

type backupCodeCountResponse struct {
	Unused int `json:"unused"`
}

type codeRequest struct {
	Code string `json:"code"`
}

// handleBeginTOTPEnrolment mints a secret and shows it once.
//
// No code is required, because there is nothing to present yet. An enrolment
// begun and never confirmed is not a second factor — TOTPEnrolment.Confirmed
// says so — so this route cannot be used to turn 2FA on, only to offer it.
func handleBeginTOTPEnrolment(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		session, ok := sessionFrom(r.Context())
		if !ok {
			writeInternalError(w, d.Logger, "enrolment outside requireAuthenticated", errNoSession)
			return
		}

		// Beginning again replaces an unfinished enrolment, which is what a
		// person who lost the QR code before scanning it needs. It does not
		// touch a confirmed one: that is what DELETE is for, and DELETE
		// requires a code.
		existing, err := d.Auth.SecondFactors.TOTPEnrolment(r.Context(), session.UserID)
		switch {
		case err != nil && !errors.Is(err, auth.ErrNoTOTPEnrolment):
			writeInternalError(w, d.Logger, "read totp enrolment", err)
			return
		case err == nil && existing.Confirmed():
			writeErrorDetail(w, d.Logger, http.StatusConflict, errorDetail{
				Code:    CodeSecondFactorAlreadySet,
				Message: "a confirmed second factor already exists; remove it before enrolling again",
			})
			return
		}

		secret, err := auth.NewSecret()
		if err != nil {
			writeInternalError(w, d.Logger, "mint totp secret", err)
			return
		}

		credentials, err := d.Auth.Credentials.CredentialsByID(r.Context(), session.UserID)
		if err != nil {
			writeInternalError(w, d.Logger, "read credentials", err)
			return
		}

		uri, err := d.Auth.Authenticator.ProvisioningURI(secret, brand.Name, credentials.Email)
		if err != nil {
			writeInternalError(w, d.Logger, "build provisioning uri", err)
			return
		}

		if err := d.Auth.SecondFactors.BeginTOTPEnrolment(
			r.Context(), session.UserID, secret, d.Auth.Now()); err != nil {
			writeInternalError(w, d.Logger, "begin totp enrolment", err)
			return
		}

		writeJSON(w, d.Logger, http.StatusCreated, beginEnrolmentResponse{
			Secret: auth.EncodeSecret(secret),
			URI:    uri,
		})
	}
}

// handleConfirmTOTPEnrolment finishes enrolment and issues backup codes.
//
// The code proves the person can produce one from the secret they were shown,
// which is the only thing that distinguishes an enrolment from a stored
// string. Confirmation happens inside the store, in the transaction that reads
// the secret, so the check and the state change cannot disagree.
func handleConfirmTOTPEnrolment(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		session, ok := sessionFrom(r.Context())
		if !ok {
			writeInternalError(w, d.Logger, "confirmation outside requireAuthenticated", errNoSession)
			return
		}

		var req codeRequest
		if !decodeBody(w, d, r, &req) {
			return
		}

		err := d.Auth.SecondFactors.ConfirmTOTPEnrolment(
			r.Context(), d.Auth.Authenticator, session.UserID, req.Code, d.Auth.Now())
		switch {
		case errors.Is(err, auth.ErrNoTOTPEnrolment):
			writeNotFound(w, d, "enrolment")
			return
		case errors.Is(err, auth.ErrTOTPAlreadyConfirmed):
			writeErrorDetail(w, d.Logger, http.StatusConflict, errorDetail{
				Code:    CodeSecondFactorAlreadySet,
				Message: "this enrolment has already been confirmed",
			})
			return
		case errors.Is(err, auth.ErrInvalidCode), errors.Is(err, auth.ErrCodeReused):
			writeInvalidCredentials(w, d)
			return
		case err != nil:
			writeInternalError(w, d.Logger, "confirm totp enrolment", err)
			return
		}

		codes, ok := issueBackupCodes(w, r, d, session.UserID)
		if !ok {
			return
		}
		recordEvent(r, d, session.UserID, "totp_enrolled", "user", session.UserID)
		writeJSON(w, d.Logger, http.StatusCreated, backupCodesResponse{Codes: codes})
	}
}

// handleDisableTOTP removes a confirmed second factor.
//
// requireSecondFactorCode has already spent a current code by the time this
// runs, so what reaches here is a request from somebody holding both the
// session and the factor.
func handleDisableTOTP(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		session, ok := sessionFrom(r.Context())
		if !ok {
			writeInternalError(w, d.Logger, "disable outside requireAuthenticated", errNoSession)
			return
		}

		if err := d.Auth.SecondFactors.DisableTOTP(r.Context(), session.UserID); err != nil {
			writeInternalError(w, d.Logger, "disable totp", err)
			return
		}
		recordEvent(r, d, session.UserID, "totp_disabled", "user", session.UserID)
		w.WriteHeader(http.StatusNoContent)
	}
}

// handleReissueBackupCodes replaces the whole set.
//
// The previous set is discarded entirely, spent codes included: somebody
// reissuing has usually decided the old list is compromised, and keeping the
// unspent half of a compromised list is keeping the problem.
func handleReissueBackupCodes(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		session, ok := sessionFrom(r.Context())
		if !ok {
			writeInternalError(w, d.Logger, "reissue outside requireAuthenticated", errNoSession)
			return
		}

		codes, ok := issueBackupCodes(w, r, d, session.UserID)
		if !ok {
			return
		}
		writeJSON(w, d.Logger, http.StatusCreated, backupCodesResponse{Codes: codes})
	}
}

// handleCountBackupCodes says how many are left and never which they are.
func handleCountBackupCodes(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		session, ok := sessionFrom(r.Context())
		if !ok {
			writeInternalError(w, d.Logger, "count outside requireAuthenticated", errNoSession)
			return
		}

		count, err := d.Auth.SecondFactors.UnusedBackupCodeCount(r.Context(), session.UserID)
		if err != nil {
			writeInternalError(w, d.Logger, "count backup codes", err)
			return
		}
		writeJSON(w, d.Logger, http.StatusOK, backupCodeCountResponse{Unused: count})
	}
}

// issueBackupCodes mints a set, stores their hashes, and returns the plaintext
// to be shown once.
func issueBackupCodes(
	w http.ResponseWriter, r *http.Request, d Deps, userID string,
) ([]string, bool) {
	codes, err := auth.NewBackupCodes()
	if err != nil {
		writeInternalError(w, d.Logger, "mint backup codes", err)
		return nil, false
	}

	hashes := make([]string, 0, len(codes))
	for _, code := range codes {
		hashes = append(hashes, auth.HashBackupCode(code))
	}
	if err := d.Auth.SecondFactors.ReplaceBackupCodes(
		r.Context(), userID, hashes, d.Auth.Now()); err != nil {
		writeInternalError(w, d.Logger, "store backup codes", err)
		return nil, false
	}
	return codes, true
}

// requireSecondFactorCode refuses a request that does not present a current
// code, and spends the one it does present.
//
// Spending it here rather than in the handler is what puts this path through
// the store's replay protection: ConsumeTOTPCode verifies and advances the
// counter inside one transaction holding the enrolment row, so a code cannot
// be used twice even by two requests arriving together. A handler calling
// Authenticator.Validate directly would check the same code and skip exactly
// that, which is the whole of the protection.
//
// A backup code is accepted in place of a one-time code, because somebody
// whose authenticator is lost is precisely who needs to disable it.
func requireSecondFactorCode(d Deps) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			session, ok := sessionFrom(r.Context())
			if !ok {
				writeInternalError(w, d.Logger, "factor check outside requireAuthenticated", errNoSession)
				return
			}

			enrolment, err := d.Auth.SecondFactors.TOTPEnrolment(r.Context(), session.UserID)
			switch {
			case errors.Is(err, auth.ErrNoTOTPEnrolment):
				// Nothing to present and nothing to protect. Refusing here
				// would make it impossible to issue a first set of backup
				// codes for an account that never enrolled.
				next.ServeHTTP(w, r)
				return
			case err != nil:
				writeInternalError(w, d.Logger, "read totp enrolment", err)
				return
			}
			if !enrolment.Confirmed() {
				next.ServeHTTP(w, r)
				return
			}

			var req codeRequest
			if !decodeBody(w, d, r, &req) {
				return
			}
			if req.Code == "" {
				writeErrorDetail(w, d.Logger, http.StatusUnauthorized, errorDetail{
					Code:    CodeSecondFactorRequired,
					Message: "changing a second factor requires a current code",
					Field:   "code",
				})
				return
			}

			err = d.Auth.SecondFactors.ConsumeTOTPCode(
				r.Context(), d.Auth.Authenticator, session.UserID, req.Code, d.Auth.Now())
			if err == nil {
				next.ServeHTTP(w, r)
				return
			}
			if !errors.Is(err, auth.ErrInvalidCode) && !errors.Is(err, auth.ErrCodeReused) {
				writeInternalError(w, d.Logger, "consume totp code", err)
				return
			}

			// Not a one-time code. It may still be a backup code, and the
			// person whose authenticator is gone has nothing else.
			if err := d.Auth.SecondFactors.ConsumeBackupCode(
				r.Context(), session.UserID, req.Code, d.Auth.Now()); err != nil {
				if !errors.Is(err, auth.ErrInvalidBackupCode) {
					writeInternalError(w, d.Logger, "consume backup code", err)
					return
				}
				writeInvalidCredentials(w, d)
				return
			}
			recordEvent(r, d, session.UserID, "backup_code_used", "user", session.UserID)
			next.ServeHTTP(w, r)
		})
	}
}
