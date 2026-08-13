// SPDX-License-Identifier: AGPL-3.0-only

package config

import (
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"time"
)

// EnvPrefix namespaces every variable this application reads, so that a shared
// host cannot leak an unrelated DATABASE_URL into it by accident.
const EnvPrefix = "NUSA_"

// Env identifies the deployment environment. It only ever loosens or tightens
// operational defaults such as log formatting; it must never change accounting
// behaviour.
type Env string

// The recognised deployment environments.
const (
	EnvDevelopment Env = "development"
	EnvProduction  Env = "production"
)

// LookupFunc resolves an environment variable. Passing it in rather than
// reading os.Getenv directly keeps this package testable without mutating
// process state.
type LookupFunc func(key string) (string, bool)

// Config is the fully resolved, validated configuration for one process.
type Config struct {
	// Env selects operational defaults. Optional, defaults to development.
	Env Env

	// HTTPAddr is the listen address for the API server.
	HTTPAddr string

	// DatabaseURL is the PostgreSQL connection string. Required; there is no
	// default, because guessing a database to connect to is never helpful.
	DatabaseURL string

	// WebDir is the directory holding the built frontend. When it is absent
	// the server still runs and serves the API — that is the normal state
	// during frontend development, where Vite serves the app instead.
	WebDir string

	// LogLevel is the minimum level emitted by the structured logger.
	LogLevel slog.Level

	// ShutdownTimeout bounds how long in-flight requests may finish after a
	// termination signal before the process exits anyway.
	ShutdownTimeout time.Duration

	// HealthTimeout bounds the database check behind the health endpoint, so
	// that an unreachable database fails the check quickly instead of leaving
	// a load balancer waiting.
	HealthTimeout time.Duration
}

// Load reads configuration from the environment and validates it.
//
// It reports every problem it finds at once. An operator with three missing
// variables should learn about all three from one failed start, not discover
// them one restart at a time.
func Load(lookup LookupFunc) (*Config, error) {
	v := &validator{lookup: lookup}

	cfg := &Config{
		Env:             Env(v.oneOf("ENV", string(EnvDevelopment), string(EnvDevelopment), string(EnvProduction))),
		HTTPAddr:        v.str("HTTP_ADDR", ":8080"),
		DatabaseURL:     v.postgresURL("DATABASE_URL"),
		WebDir:          v.str("WEB_DIR", "web/dist"),
		LogLevel:        v.logLevel("LOG_LEVEL", slog.LevelInfo),
		ShutdownTimeout: v.duration("SHUTDOWN_TIMEOUT", 15*time.Second),
		HealthTimeout:   v.duration("HEALTH_TIMEOUT", 2*time.Second),
	}

	if err := v.err(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// RedactedDatabaseURL returns the connection string with its password removed,
// so it can be logged. Never log DatabaseURL itself.
func (c *Config) RedactedDatabaseURL() string {
	u, err := url.Parse(c.DatabaseURL)
	if err != nil {
		return "(unparseable)"
	}
	if u.User != nil {
		if _, hasPassword := u.User.Password(); hasPassword {
			u.User = url.UserPassword(u.User.Username(), "xxxxx")
		}
	}
	return u.String()
}

// IsDevelopment reports whether operational defaults should favour a human at
// a terminal over a log aggregator.
func (c *Config) IsDevelopment() bool { return c.Env == EnvDevelopment }

// validator accumulates problems instead of returning on the first one.
type validator struct {
	lookup   LookupFunc
	problems []string
}

func (v *validator) get(name string) (string, bool) {
	raw, ok := v.lookup(EnvPrefix + name)
	raw = strings.TrimSpace(raw)
	return raw, ok && raw != ""
}

func (v *validator) reject(name, format string, args ...any) {
	v.problems = append(v.problems, fmt.Sprintf(EnvPrefix+name+": "+format, args...))
}

func (v *validator) str(name, fallback string) string {
	if raw, ok := v.get(name); ok {
		return raw
	}
	return fallback
}

func (v *validator) oneOf(name, fallback string, allowed ...string) string {
	raw, ok := v.get(name)
	if !ok {
		return fallback
	}
	for _, candidate := range allowed {
		if raw == candidate {
			return raw
		}
	}
	v.reject(name, "unknown value %q (want one of: %s)", raw, strings.Join(allowed, ", "))
	return fallback
}

func (v *validator) duration(name string, fallback time.Duration) time.Duration {
	raw, ok := v.get(name)
	if !ok {
		return fallback
	}
	parsed, err := time.ParseDuration(raw)
	switch {
	case err != nil:
		v.reject(name, "%q is not a duration (want something like 15s or 2m)", raw)
	case parsed <= 0:
		v.reject(name, "must be positive, got %s", parsed)
	default:
		return parsed
	}
	return fallback
}

func (v *validator) logLevel(name string, fallback slog.Level) slog.Level {
	raw, ok := v.get(name)
	if !ok {
		return fallback
	}
	var level slog.Level
	if err := level.UnmarshalText([]byte(raw)); err != nil {
		v.reject(name, "unknown level %q (want one of: debug, info, warn, error)", raw)
		return fallback
	}
	return level
}

func (v *validator) postgresURL(name string) string {
	raw, ok := v.get(name)
	if !ok {
		v.reject(name, "is required (PostgreSQL connection string, "+
			"e.g. postgres://nusa:secret@localhost:5432/nusa?sslmode=disable)")
		return ""
	}

	u, err := url.Parse(raw)
	if err != nil {
		// The error text from url.Parse can echo the password back, so it is
		// deliberately not included here.
		v.reject(name, "is not a valid URL")
		return ""
	}
	switch u.Scheme {
	case "postgres", "postgresql":
	default:
		v.reject(name, "scheme must be postgres:// or postgresql://, got %q", u.Scheme)
	}
	if u.Host == "" {
		v.reject(name, "is missing a host")
	}
	if strings.Trim(u.Path, "/") == "" {
		v.reject(name, "is missing a database name")
	}
	return raw
}

func (v *validator) err() error {
	if len(v.problems) == 0 {
		return nil
	}
	return &Error{Problems: v.problems}
}

// Error reports every configuration problem found during a single load.
type Error struct {
	Problems []string
}

func (e *Error) Error() string {
	var b strings.Builder
	b.WriteString("invalid configuration:")
	for _, p := range e.Problems {
		b.WriteString("\n  - ")
		b.WriteString(p)
	}
	b.WriteString("\n\nSee .env.example for every supported variable.")
	return b.String()
}
