// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/nusa-app/nusa/internal/brand"
)

// DatabaseChecker reports whether the database is reachable and which schema
// version it carries.
//
// It is declared here, where it is consumed, rather than in the store package
// that implements it. That keeps this package independent of the persistence
// layer and trivially fakeable in tests.
type DatabaseChecker interface {
	CheckDatabase(ctx context.Context) (schemaVersion int32, err error)
}

const (
	statusOK          = "ok"
	statusUnavailable = "unavailable"
)

type healthResponse struct {
	Status  string       `json:"status"`
	Service string       `json:"service"`
	Checks  healthChecks `json:"checks"`
}

type healthChecks struct {
	Database databaseCheck `json:"database"`
}

type databaseCheck struct {
	Status string `json:"status"`
	// SchemaVersion is absent rather than zero when the check failed, so a
	// consumer cannot mistake "unknown" for "version 0".
	SchemaVersion *int32 `json:"schema_version,omitempty"`
}

// handleHealth reports whether this process can serve traffic.
//
// The endpoint is unauthenticated, so the response says only whether the
// database answered and at which schema version. The underlying error goes to
// the log, never to the client: connection errors routinely contain hostnames
// and usernames.
func handleHealth(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), d.HealthTimeout)
		defer cancel()

		body := healthResponse{
			Status:  statusOK,
			Service: brand.Slug,
			Checks:  healthChecks{Database: databaseCheck{Status: statusOK}},
		}
		status := http.StatusOK

		version, err := d.Database.CheckDatabase(ctx)
		if err != nil {
			d.Logger.ErrorContext(ctx, "health check: database unreachable",
				slog.String("error", err.Error()))

			body.Status = statusUnavailable
			body.Checks.Database.Status = statusUnavailable
			status = http.StatusServiceUnavailable
		} else {
			body.Checks.Database.SchemaVersion = &version
		}

		writeJSON(w, d.Logger, status, body)
	}
}
