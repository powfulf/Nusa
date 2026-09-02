// SPDX-License-Identifier: AGPL-3.0-only

package api_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/powfulf/Nusa/internal/api"
)

// stubDatabase stands in for the store so the HTTP layer can be tested without
// a database.
type stubDatabase struct {
	version int32
	err     error
}

func (s stubDatabase) CheckDatabase(context.Context) (int32, error) {
	return s.version, s.err
}

// getHealthz builds the request under test.
//
// http.NewRequestWithContext rather than httptest.NewRequest, because the
// latter's context-carrying form needs a newer Go than this module's floor.
func getHealthz(t *testing.T) *http.Request {
	t.Helper()

	req, err := http.NewRequestWithContext(
		context.Background(), http.MethodGet, "http://example.test/healthz", nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	return req
}

func newRouter(t *testing.T, db api.DatabaseChecker) http.Handler {
	t.Helper()
	return api.NewRouter(api.Deps{
		// Discard log output: these tests assert on responses, and a failing
		// health check is expected to log loudly.
		Logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
		Database:      db,
		HealthTimeout: time.Second,
	})
}

func TestHealthzReportsSchemaVersion(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	newRouter(t, stubDatabase{version: 1}).ServeHTTP(rec, getHealthz(t))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var body struct {
		Status  string `json:"status"`
		Service string `json:"service"`
		Checks  struct {
			Database struct {
				Status        string `json:"status"`
				SchemaVersion *int32 `json:"schema_version"`
			} `json:"database"`
		} `json:"checks"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if body.Status != "ok" {
		t.Errorf("status = %q, want %q", body.Status, "ok")
	}
	if body.Checks.Database.Status != "ok" {
		t.Errorf("database status = %q, want %q", body.Checks.Database.Status, "ok")
	}
	if body.Checks.Database.SchemaVersion == nil || *body.Checks.Database.SchemaVersion != 1 {
		t.Errorf("schema_version = %v, want 1", body.Checks.Database.SchemaVersion)
	}
	if body.Service == "" {
		t.Error("service is empty, want the product slug")
	}
}

func TestHealthzFailsWhenDatabaseIsUnreachable(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	db := stubDatabase{err: errors.New("dial tcp 10.0.0.5:5432: connection refused")}
	newRouter(t, db).ServeHTTP(rec, getHealthz(t))

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}

	// The endpoint is unauthenticated. A connection error naming an internal
	// host must never reach the client.
	if body := rec.Body.String(); strings.Contains(body, "10.0.0.5") || strings.Contains(body, "connection refused") {
		t.Errorf("response leaked the underlying error: %s", body)
	}
	if !strings.Contains(rec.Body.String(), "unavailable") {
		t.Errorf("response does not report the failure: %s", rec.Body.String())
	}
}

func TestHealthzReportsNoSchemaVersionOnFailure(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	db := stubDatabase{err: errors.New("boom")}
	newRouter(t, db).ServeHTTP(rec, getHealthz(t))

	// Omitted rather than zero, so a consumer cannot read "unknown" as
	// "schema version 0".
	if strings.Contains(rec.Body.String(), "schema_version") {
		t.Errorf("schema_version present despite a failed check: %s", rec.Body.String())
	}
}
