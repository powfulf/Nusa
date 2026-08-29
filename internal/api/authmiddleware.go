// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/GaffaQ/Nusa/internal/auth"
)

// Session resolution, per request.
//
// # Nothing is cached, and that is the property this layer adds
//
// The store proves that a revoked session stops resolving. What only this
// layer can prove is that the store is asked *every time* — a middleware that
// held sessions in memory for even a few seconds would leave a revoked token
// working for that long, and the store's test would still pass.
//
// So there is no cache here, and there will not be one without a mechanism to
// invalidate it. The cost is one indexed lookup per request against a unique
// index on token_hash, which is the cheapest thing a request does.
//
// # What this does not do
//
// It does not interrupt a handler that has already started. A request that
// passed through here before the revocation committed runs to completion, and
// nothing short of cancelling its context mid-flight would change that.
//
// That is stated rather than hidden because it is a real limit, and it is
// acceptable for the shape of request Nusa serves: every endpoint is a short
// read or write, so the window is milliseconds. It stops being acceptable the
// day there is a long-lived endpoint — a server-sent event stream, a long poll
// — and that is when a watcher that cancels the request context on revocation
// earns the goroutine it costs. Adding one now would put a goroutine on every
// request to close a window nothing can currently hold open.

type contextKey int

const (
	sessionContextKey contextKey = iota
	idempotencyKeyContextKey
)

// sessionFrom returns the session a request was authenticated with.
//
// It is only meaningful inside a handler that requireSession wrapped; anywhere
// else it reports false, which is a programming error rather than an
// authentication failure and is treated as one.
func sessionFrom(ctx context.Context) (auth.Session, bool) {
	s, ok := ctx.Value(sessionContextKey).(auth.Session)
	return s, ok
}

// requireSession resolves the cookie and refuses the request if it names no
// usable session.
//
// A session awaiting its second factor *is* usable here and is passed through.
// That is not an oversight: it is the credential the second step presents, and
// refusing it at this layer would make two-factor authentication impossible to
// complete. What stops it reaching anything else is requireAuthenticated,
// which every other route is wrapped in.
func requireSession(d Deps) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token := sessionToken(r, d.Auth.SecureCookies)
			if token == "" {
				writeError(w, d.Logger, http.StatusUnauthorized, CodeUnauthenticated, "no session")
				return
			}

			session, err := d.Auth.Sessions.SessionByToken(r.Context(), token, d.Auth.now())
			switch {
			case errors.Is(err, auth.ErrNoSession), errors.Is(err, auth.ErrSessionNotUsable):
				// Both answer the same way. A token that never existed and one
				// that has been revoked are different facts to an operator and
				// the same fact to whoever is holding the cookie: sign in
				// again. The log keeps the distinction.
				d.Logger.InfoContext(r.Context(), "session rejected",
					slog.String("reason", err.Error()),
					slog.String("client_ip", clientIPString(r, d)))
				clearSessionCookie(w, d.Auth.SecureCookies)
				writeError(w, d.Logger, http.StatusUnauthorized, CodeUnauthenticated, "no usable session")
				return
			case err != nil:
				writeInternalError(w, d.Logger, "resolve session", err)
				return
			}

			ctx := context.WithValue(r.Context(), sessionContextKey, session)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// requireAuthenticated additionally refuses a session that has not satisfied
// its second factor.
//
// Every route except the second-factor endpoint and sign-out is wrapped in
// this. A pending session holds a live cookie, so without it that cookie would
// be a fully authenticated credential that merely had not been asked for a
// code — which is the whole of two-factor authentication undone.
func requireAuthenticated(d Deps) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return requireSession(d)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			session, ok := sessionFrom(r.Context())
			if !ok {
				writeInternalError(w, d.Logger, "session missing from context",
					errors.New("requireAuthenticated used outside requireSession"))
				return
			}
			if session.PendingSecondFactor() {
				writeErrorDetail(w, d.Logger, http.StatusUnauthorized, errorDetail{
					Code:    CodeSecondFactorRequired,
					Message: "this session has not completed its second factor",
				})
				return
			}
			next.ServeHTTP(w, r)
		}))
	}
}

// clientIPString renders the address a request is attributed to, for logs and
// for the rate limiter's key.
//
// An unparseable peer produces "unknown" rather than an empty string, so that
// every attempt lands in some bucket. Sharing one bucket is the right failure:
// a peer that cannot be parsed is not a peer that should get an unlimited
// allowance.
func clientIPString(r *http.Request, d Deps) string {
	addr := ClientIP(r, d.Auth.TrustedProxies)
	if !addr.IsValid() {
		return "unknown"
	}
	return addr.String()
}
