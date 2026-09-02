// SPDX-License-Identifier: AGPL-3.0-only

package config

import (
	"fmt"
	"log/slog"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/powfulf/Nusa/internal/auth"
)

// This package imports internal/auth, and the dependency runs only that way:
// a depguard rule refuses internal/config inside internal/auth, so the cycle
// cannot form. The reason to import at all is the Argon2id floor below. Config
// has to refuse a cost weaker than policy, and expressing that against
// auth.DefaultParams() rather than against a copy of the numbers means there is
// one definition of what the cost is, instead of two that drift.

// EnvPrefix namespaces every variable this application reads, so that a shared
// host cannot leak an unrelated DATABASE_URL into it by accident.
const EnvPrefix = "NUSA_"

// Env identifies the deployment environment.
//
// It loosens or tightens operational defaults such as log formatting, and it
// decides one security setting: SecureCookies. It must never change accounting
// behaviour — a figure computed in development and the same figure computed in
// production are the same figure.
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

	// TrustedProxies are the networks whose X-Forwarded-For header may be
	// believed. Empty — the default — means the header is ignored entirely and
	// every request is attributed to the address it actually arrived from.
	//
	// There is no value that means "trust everything": see the validator.
	TrustedProxies []netip.Prefix

	// SessionTTL is how long a sign-in lasts before it must be repeated,
	// regardless of activity.
	SessionTTL time.Duration

	// PasswordHashing is the Argon2id cost new passwords are minted at. It may
	// be raised above policy and never lowered below it.
	PasswordHashing auth.Params

	// LoginFailureLimit and LoginFailureWindow bound sign-in attempts per
	// client address. See auth.Limiter for why the account is not part of the
	// key.
	LoginFailureLimit  int
	LoginFailureWindow time.Duration
}

// Load reads configuration from the environment and validates it.
//
// It reports every problem it finds at once. An operator with three missing
// variables should learn about all three from one failed start, not discover
// them one restart at a time.
func Load(lookup LookupFunc) (*Config, error) {
	v := &validator{lookup: lookup}
	limitDefault, windowDefault := auth.LimiterDefaults()

	cfg := &Config{
		Env:             Env(v.oneOf("ENV", string(EnvDevelopment), string(EnvDevelopment), string(EnvProduction))),
		HTTPAddr:        v.str("HTTP_ADDR", ":8080"),
		DatabaseURL:     v.postgresURL("DATABASE_URL"),
		WebDir:          v.str("WEB_DIR", "web/dist"),
		LogLevel:        v.logLevel("LOG_LEVEL", slog.LevelInfo),
		ShutdownTimeout: v.duration("SHUTDOWN_TIMEOUT", 15*time.Second),
		HealthTimeout:   v.duration("HEALTH_TIMEOUT", 2*time.Second),

		TrustedProxies: v.cidrList("TRUSTED_PROXIES"),
		SessionTTL:     v.duration("SESSION_TTL", 30*24*time.Hour),

		PasswordHashing: v.argon2Params(),

		LoginFailureLimit:  v.atLeast("LOGIN_FAILURE_LIMIT", limitDefault, 1),
		LoginFailureWindow: v.duration("LOGIN_FAILURE_WINDOW", windowDefault),
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

// SecureCookies reports whether session cookies must carry the Secure
// attribute.
//
// It is derived from Env and reads no variable of its own, and that is the
// whole design. A flag that can be turned off is a flag that gets turned off in
// production by somebody in a hurry, at three in the morning, to make a
// misconfigured reverse proxy work — and the cookie it exposes then travels in
// clear over every subsequent request. There is deliberately no
// NUSA_COOKIE_SECURE, no NUSA_INSECURE_COOKIES and no override of any spelling:
// in production this is true and nothing in the environment can change it.
//
// In development it is false, because a Secure cookie is not sent over
// http://localhost by every browser and the alternative would be that nobody
// can sign in while working on the frontend. That asymmetry is the only reason
// Env touches this at all.
func (c *Config) SecureCookies() bool { return c.Env == EnvProduction }

// atLeast reads a positive integer with a hard floor.
func (v *validator) atLeast(name string, fallback, floor int) int {
	raw, ok := v.get(name)
	if !ok {
		return fallback
	}
	parsed, err := strconv.Atoi(raw)
	switch {
	case err != nil:
		v.reject(name, "%q is not a whole number", raw)
	case parsed < floor:
		v.reject(name, "must be at least %d, got %d", floor, parsed)
	default:
		return parsed
	}
	return fallback
}

// argon2Params reads the password hashing cost.
//
// **The cost may be raised and never lowered.** auth.DefaultParams is the floor
// as well as the default, so every one of these variables is one-directional.
//
// The reason is the one that applies to every security parameter with a knob on
// it: a number that can be turned down is a number that eventually gets turned
// down, usually by somebody trying to make a slow test suite or a small
// container behave, and the result is a password store that is weaker than the
// project believes it is with nothing anywhere saying so. Raising the cost is a
// decision an operator can make about their own hardware. Lowering it is a
// decision about everybody's passwords, and it is not on offer.
//
// The floor is not arbitrary either — DefaultParams was already derived from
// the weakest host Nusa targets, so there is no legitimate deployment that
// needs less. See the measurement recorded there.
func (v *validator) argon2Params() auth.Params {
	floor := auth.DefaultParams()
	p := floor

	p.Memory = v.costAtLeast("ARGON2_MEMORY_KIB", floor.Memory, auth.MaxVerifiableMemory)
	p.Iterations = v.costAtLeast("ARGON2_ITERATIONS", floor.Iterations, maxArgon2Iterations)
	// Bounded to maxArgon2Parallelism by the call above, so the narrowing is
	// provably safe; gosec's rule is syntactic and cannot see the ceiling.
	lanes := v.costAtLeast("ARGON2_PARALLELISM", uint32(floor.Parallelism), maxArgon2Parallelism)
	p.Parallelism = uint8(lanes) //nolint:gosec // bounded by maxArgon2Parallelism above

	return p
}

// maxArgon2Iterations and maxArgon2Parallelism cap what may be asked for.
//
// A ceiling is not paternalism, it is typo protection: ARGON2_ITERATIONS=200
// instead of 20 turns every sign-in into a multi-second stall, and the symptom
// (the application appears to hang on login) points nowhere near the cause. The
// memory ceiling is auth.MaxVerifiableMemory rather than a number chosen here,
// because minting above it would produce hashes that auth.Verify then refuses —
// the credential store would fill with rows nothing can check.
const (
	maxArgon2Iterations  = 32
	maxArgon2Parallelism = 16
)

// costAtLeast reads a cost parameter that may only move upward from floor.
//
// It returns uint32 rather than int so that no call site has to narrow a value
// whose range was already established here.
func (v *validator) costAtLeast(name string, floor, ceiling uint32) uint32 {
	raw, ok := v.get(name)
	if !ok {
		return floor
	}
	parsed, err := strconv.ParseUint(raw, 10, 32)
	switch {
	case err != nil:
		v.reject(name, "%q is not a whole number within range", raw)
	case parsed < uint64(floor):
		v.reject(name, "may be raised above the default but never lowered: "+
			"%d is below the floor of %d", parsed, floor)
	case parsed > uint64(ceiling):
		v.reject(name, "%d exceeds the maximum of %d", parsed, ceiling)
	default:
		// ParseUint with a bit size of 32 has already established the range.
		return uint32(parsed)
	}
	return floor
}

// cidrList reads a comma-separated list of networks whose forwarded-for header
// may be believed.
//
// Every rejection below exists because the alternative is guessing at what an
// operator meant, and this is a list that decides whose claim about a client's
// identity is accepted. A configuration mistake here does not produce an error
// later; it produces a system that quietly attributes requests to whatever
// address an attacker types.
func (v *validator) cidrList(name string) []netip.Prefix {
	raw, ok := v.get(name)
	if !ok {
		// The default is to trust nothing, which makes the header irrelevant.
		// That is the correct default for a self-hosted application reached
		// directly, and the only safe one for an operator who has not thought
		// about it.
		return nil
	}

	var out []netip.Prefix
	seen := make(map[netip.Prefix]bool)

	for _, field := range strings.Split(raw, ",") {
		entry := strings.TrimSpace(field)
		if entry == "" {
			// Refused rather than skipped. A trailing comma or a doubled one
			// is a sign the value was assembled by something that got it
			// wrong, and silently accepting the rest hides that.
			v.reject(name, "contains an empty entry (a stray or doubled comma in %q)", raw)
			continue
		}

		prefix, err := parseTrustedPrefix(entry)
		if err != nil {
			v.reject(name, "%s", err)
			continue
		}
		if seen[prefix] {
			v.reject(name, "lists %s more than once", prefix)
			continue
		}

		seen[prefix] = true
		out = append(out, prefix)
	}
	return out
}

// parseTrustedPrefix reads one entry of the trusted proxy list.
func parseTrustedPrefix(entry string) (netip.Prefix, error) {
	if !strings.Contains(entry, "/") {
		// A bare address is unambiguous: one address means one address. It is
		// accepted and widened to a single-host prefix, because requiring
		// /32 on every entry would be ceremony for the commonest case, which
		// is one reverse proxy on one address.
		addr, err := netip.ParseAddr(entry)
		if err != nil {
			return netip.Prefix{}, fmt.Errorf("%q is neither an IP address nor a CIDR range", entry)
		}
		if addr.Zone() != "" {
			return netip.Prefix{}, fmt.Errorf("%q carries a zone, which cannot be matched against a request", entry)
		}
		addr = addr.Unmap()
		return netip.PrefixFrom(addr, addr.BitLen()), nil
	}

	prefix, err := netip.ParsePrefix(entry)
	if err != nil {
		return netip.Prefix{}, fmt.Errorf("%q is not a valid CIDR range", entry)
	}
	if prefix.Addr().Zone() != "" {
		return netip.Prefix{}, fmt.Errorf("%q carries a zone, which cannot be matched against a request", entry)
	}

	// Host bits set is the ambiguous case, and the one worth refusing loudest.
	// 10.0.0.1/24 reads as "this host" to one person and "this network" to
	// another, and ParsePrefix resolves it by silently masking to 10.0.0.0/24 —
	// which is 256 addresses when the operator may have meant one. Refusing
	// costs them one character; guessing costs them a network they did not
	// intend to trust.
	if prefix.Masked() != prefix {
		return netip.Prefix{}, fmt.Errorf(
			"%q has bits set below its prefix length: write %s for the network, or %s for the single host",
			entry, prefix.Masked(), prefix.Addr())
	}

	// A zero-length prefix trusts every client on the internet to state its own
	// address, which is the blanket header trust §10 exists to forbid — and
	// worse than not configuring proxies at all, because it looks deliberate.
	// Anyone reaching for this wants to trust their proxy, and their proxy has
	// an address.
	if prefix.Bits() == 0 {
		return netip.Prefix{}, fmt.Errorf(
			"%q would trust every client to state its own address, which defeats the point of the list; "+
				"name the proxy's address instead", entry)
	}

	// An IPv4 range written in IPv6 form is refused rather than converted.
	// ::ffff:10.0.0.0/104 and 10.0.0.0/8 are the same network, but the prefix
	// lengths differ by 96 and converting between them by arithmetic is exactly
	// the sort of quiet reinterpretation this validator exists to avoid. A
	// bare ::ffff:10.0.0.1 is accepted above, because a single address has no
	// length to get wrong.
	if prefix.Addr().Is4In6() {
		return netip.Prefix{}, fmt.Errorf(
			"%q writes an IPv4 network in IPv6 form; write it as %s/%d instead",
			entry, prefix.Addr().Unmap(), prefix.Bits()-96)
	}

	return prefix, nil
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
