// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
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
