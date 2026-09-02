// SPDX-License-Identifier: AGPL-3.0-only

package config_test

import (
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/powfulf/Nusa/internal/config"
)

// env builds a LookupFunc over a literal map, so tests never touch process
// environment state and can run in parallel.
func env(pairs map[string]string) config.LookupFunc {
	return func(key string) (string, bool) {
		v, ok := pairs[key]
		return v, ok
	}
}

const validDSN = "postgres://nusa:secret@localhost:5432/nusa?sslmode=disable"

func TestLoadAppliesDefaults(t *testing.T) {
	t.Parallel()

	cfg, err := config.Load(env(map[string]string{
		"NUSA_DATABASE_URL": validDSN,
	}))
	if err != nil {
		t.Fatalf("Load() returned an error for a minimal valid environment: %v", err)
	}

	if cfg.Env != config.EnvDevelopment {
		t.Errorf("Env = %q, want %q", cfg.Env, config.EnvDevelopment)
	}
	if cfg.HTTPAddr != ":8080" {
		t.Errorf("HTTPAddr = %q, want %q", cfg.HTTPAddr, ":8080")
	}
	if cfg.LogLevel != slog.LevelInfo {
		t.Errorf("LogLevel = %v, want %v", cfg.LogLevel, slog.LevelInfo)
	}
	if cfg.ShutdownTimeout != 15*time.Second {
		t.Errorf("ShutdownTimeout = %v, want %v", cfg.ShutdownTimeout, 15*time.Second)
	}
}

func TestLoadOverrides(t *testing.T) {
	t.Parallel()

	cfg, err := config.Load(env(map[string]string{
		"NUSA_DATABASE_URL":     validDSN,
		"NUSA_ENV":              "production",
		"NUSA_HTTP_ADDR":        ":9000",
		"NUSA_LOG_LEVEL":        "warn",
		"NUSA_SHUTDOWN_TIMEOUT": "45s",
		"NUSA_WEB_DIR":          "/srv/web",
	}))
	if err != nil {
		t.Fatalf("Load() returned an error: %v", err)
	}

	if cfg.Env != config.EnvProduction || cfg.IsDevelopment() {
		t.Errorf("Env = %q, want %q", cfg.Env, config.EnvProduction)
	}
	if cfg.HTTPAddr != ":9000" {
		t.Errorf("HTTPAddr = %q, want %q", cfg.HTTPAddr, ":9000")
	}
	if cfg.LogLevel != slog.LevelWarn {
		t.Errorf("LogLevel = %v, want %v", cfg.LogLevel, slog.LevelWarn)
	}
	if cfg.ShutdownTimeout != 45*time.Second {
		t.Errorf("ShutdownTimeout = %v, want %v", cfg.ShutdownTimeout, 45*time.Second)
	}
	if cfg.WebDir != "/srv/web" {
		t.Errorf("WebDir = %q, want %q", cfg.WebDir, "/srv/web")
	}
}

func TestLoadRequiresDatabaseURL(t *testing.T) {
	t.Parallel()

	_, err := config.Load(env(nil))
	if err == nil {
		t.Fatal("Load() succeeded with no database URL, want an error")
	}
	if !strings.Contains(err.Error(), "NUSA_DATABASE_URL") {
		t.Errorf("error does not name the missing variable: %v", err)
	}
}

func TestLoadReportsEveryProblemAtOnce(t *testing.T) {
	t.Parallel()

	_, err := config.Load(env(map[string]string{
		"NUSA_ENV":              "staging",
		"NUSA_LOG_LEVEL":        "verbose",
		"NUSA_SHUTDOWN_TIMEOUT": "soon",
	}))
	if err == nil {
		t.Fatal("Load() succeeded with three bad values, want an error")
	}

	var cfgErr *config.Error
	if !errors.As(err, &cfgErr) {
		t.Fatalf("error is %T, want *config.Error", err)
	}

	// One problem per bad variable, plus the missing database URL. Reporting
	// them together is the point: an operator fixes one round, not four.
	if len(cfgErr.Problems) != 4 {
		t.Errorf("got %d problems, want 4: %v", len(cfgErr.Problems), cfgErr.Problems)
	}
	for _, want := range []string{"NUSA_ENV", "NUSA_LOG_LEVEL", "NUSA_SHUTDOWN_TIMEOUT", "NUSA_DATABASE_URL"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not mention %s: %v", want, err)
		}
	}
}

func TestLoadRejectsNonPostgresURL(t *testing.T) {
	t.Parallel()

	for name, dsn := range map[string]string{
		"wrong scheme":       "mysql://nusa:secret@localhost:3306/nusa",
		"missing host":       "postgres:///nusa",
		"missing database":   "postgres://nusa:secret@localhost:5432/",
		"sqlite is not a db": "file:nusa.sqlite",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := config.Load(env(map[string]string{"NUSA_DATABASE_URL": dsn})); err == nil {
				t.Errorf("Load() accepted %q, want an error", dsn)
			}
		})
	}
}

func TestRedactedDatabaseURLHidesPassword(t *testing.T) {
	t.Parallel()

	cfg, err := config.Load(env(map[string]string{"NUSA_DATABASE_URL": validDSN}))
	if err != nil {
		t.Fatalf("Load() returned an error: %v", err)
	}

	redacted := cfg.RedactedDatabaseURL()
	if strings.Contains(redacted, "secret") {
		t.Errorf("RedactedDatabaseURL() leaked the password: %q", redacted)
	}
	if !strings.Contains(redacted, "localhost:5432") {
		t.Errorf("RedactedDatabaseURL() dropped the host, leaving it useless: %q", redacted)
	}
}
