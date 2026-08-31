// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/GaffaQ/Nusa/internal/ledger"
)

// Deps are everything the HTTP layer needs from the outside. Passing them
// explicitly keeps this package free of global state and easy to test.
type Deps struct {
	// Logger receives request logs. Required.
	Logger *slog.Logger

	// Database backs the health endpoint. Required.
	Database DatabaseChecker

	// HealthTimeout bounds the database check so an unreachable database fails
	// the health endpoint quickly instead of hanging a load balancer.
	HealthTimeout time.Duration

	// WebDir holds the built frontend. When empty or absent, the server runs
	// API-only — the normal state during frontend development, where Vite
	// serves the app and proxies here.
	WebDir string

	// Auth carries everything the authentication endpoints need. When it is
	// absent or incomplete those routes are not mounted at all, which is a
	// louder failure than mounting them and dereferencing nil on somebody's
	// first sign-in.
	Auth *AuthDeps

	// Ledger carries what the ledger endpoints need. Absent or incomplete,
	// those routes are not mounted, for the same reason Auth is not: a route
	// that dereferences nil on somebody's first write is a quieter failure
	// than a route that was never there.
	Ledger *LedgerDeps
}

// NewRouter assembles the HTTP handler.
func NewRouter(d Deps) http.Handler {
	r := chi.NewRouter()

	r.Use(middleware.RequestID)
	// Deliberately no RealIP middleware: it trusts X-Forwarded-For and friends
	// unconditionally, which lets a client forge its own address. Nothing here
	// uses the client IP yet. When rate limiting arrives in M2 it needs an
	// explicit list of trusted proxies rather than blanket header trust.
	r.Use(requestLogger(d.Logger))
	r.Use(middleware.Recoverer)

	r.Get("/healthz", handleHealth(d))

	if d.Auth.ready() {
		r.Route("/api/v1/auth", func(r chi.Router) {
			r.Post("/register", handleRegister(d))
			r.Post("/login", handleLogin(d))

			// The two routes a session awaiting its second factor may reach,
			// and the only two. Both are wrapped in requireSession rather than
			// requireAuthenticated, because a pending session is precisely the
			// credential they act on.
			r.Group(func(r chi.Router) {
				r.Use(requireSession(d))
				r.Post("/second-factor", handleSecondFactor(d))
				r.Post("/logout", handleLogout(d))
			})

			// Everything else. requireAuthenticated refuses a pending session,
			// so a cookie handed out at the password step cannot reach any of
			// it without a code.
			r.Group(func(r chi.Router) {
				r.Use(requireAuthenticated(d))
				r.Get("/session", handleCurrentSession(d))

				// Setting up a second factor. Beginning an enrolment and
				// confirming it need only the session — there is nothing to
				// present until one exists.
				r.Post("/totp", handleBeginTOTPEnrolment(d))
				r.Post("/totp/confirm", handleConfirmTOTPEnrolment(d))
				r.Get("/backup-codes", handleCountBackupCodes(d))

				// Changing or removing one needs a current code as well. The
				// requirement lives here, once, rather than inside each
				// handler: secondFactorRoutes is the table a guard walks, and
				// a route added below without being added there is what that
				// guard is for.
				r.Group(func(r chi.Router) {
					r.Use(requireSecondFactorCode(d))

					r.Delete("/totp", handleDisableTOTP(d))
					r.Post("/backup-codes", handleReissueBackupCodes(d))
				})
			})
		})
	} else {
		d.Logger.Warn("authentication routes not mounted: dependencies incomplete")
	}

	if d.Auth.ready() && d.Ledger.ready() {
		// Every ledger route sits behind requireAuthenticated, so a session
		// that has passed the password step but not its second factor reaches
		// none of it.
		r.Route("/api/v1", func(r chi.Router) {
			r.Use(requireAuthenticated(d))

			r.Get("/commodities", handleListCommodities(d))
			r.Get("/accounts", handleListAccounts(d))
			r.Get("/accounts/{id}", handleGetAccount(d))
			r.Get("/transactions", handleListTransactions(d))
			r.Get("/transactions/{id}", handleGetTransaction(d))

			// Every mutation, and nothing else, requires the key. A GET that
			// demanded one would be asking a caller to name a retry of
			// something that wrote nothing.
			r.Group(func(r chi.Router) {
				r.Use(requireIdempotencyKey(d))

				r.Post("/accounts", handleCreateAccount(d))
				r.Patch("/accounts/{id}", handlePatchAccount(d))
				r.Post("/transactions", handleCreateTransaction(d))

				// A correction and a deletion are one mechanism with two
				// names, so they are two routes into one handler rather than
				// two handlers. There is no DELETE: a reversal needs an
				// identity, a date and a map of posting identities, so it
				// could never have been bodiless, and a body on DELETE is
				// undefined rather than merely unusual — an endpoint resting
				// on it fails by environment rather than by logic.
				r.Post("/transactions/{id}/corrections", handleReversal(d, ledger.Correction))
				r.Post("/transactions/{id}/deletions", handleReversal(d, ledger.Deletion))
			})
		})
	} else {
		d.Logger.Warn("ledger routes not mounted: dependencies incomplete")
	}

	if static, ok := staticHandler(d.WebDir); ok {
		r.NotFound(static)
	} else {
		d.Logger.Info("serving api only, no frontend build found", slog.String("web_dir", d.WebDir))
	}

	return r
}

// requestLogger records one structured line per request.
//
// It logs the path but never the query string or any header: query parameters
// are the easiest place for sensitive values to end up in a log aggregator by
// accident.
func requestLogger(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			wrapped := middleware.NewWrapResponseWriter(w, r.ProtoMajor)

			next.ServeHTTP(wrapped, r)

			logger.LogAttrs(r.Context(), slog.LevelInfo, "http request",
				slog.String("request_id", middleware.GetReqID(r.Context())),
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
				slog.Int("status", wrapped.Status()),
				slog.Int("bytes", wrapped.BytesWritten()),
				slog.Duration("duration", time.Since(start)),
			)
		})
	}
}

// writeJSON serialises a response body. Encoding failures are logged rather
// than surfaced, because the status line has already been written by then.
func writeJSON(w http.ResponseWriter, logger *slog.Logger, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)

	if err := json.NewEncoder(w).Encode(body); err != nil {
		logger.Error("encode response body", slog.String("error", err.Error()))
	}
}
