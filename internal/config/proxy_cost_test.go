// SPDX-License-Identifier: AGPL-3.0-only

package config_test

import (
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/powfulf/Nusa/internal/auth"
	"github.com/powfulf/Nusa/internal/config"
)

// What these guards must cover. Written before they were, and each item was
// broken on purpose and watched failing afterwards, against a prediction
// written first (CLAUDE.md §11).
//
//	1. The defaults: no trusted proxies, cost equal to policy, limiter defaults.
//	2. Every unambiguous CIDR form is accepted, and normalised the same way.
//	3. Every ambiguous or dangerous form is refused rather than interpreted.
//	4. Argon2id cost may be raised and never lowered, and is capped.
//	5. Secure cookies are on in production and no variable can turn them off.

// withEnv builds a valid minimal environment plus whatever a test adds.
func withEnv(extra map[string]string) config.LookupFunc {
	pairs := map[string]string{"NUSA_DATABASE_URL": validDSN}
	for k, v := range extra {
		pairs[k] = v
	}
	return env(pairs)
}

func loadOK(t *testing.T, extra map[string]string) *config.Config {
	t.Helper()
	cfg, err := config.Load(withEnv(extra))
	if err != nil {
		t.Fatalf("Load() = %v, want success", err)
	}
	return cfg
}

func loadProblem(t *testing.T, extra map[string]string) string {
	t.Helper()
	_, err := config.Load(withEnv(extra))
	if err == nil {
		t.Fatalf("Load() succeeded, want a rejection")
	}
	return err.Error()
}

// 1.
func TestSecurityDefaults(t *testing.T) {
	t.Parallel()
	cfg := loadOK(t, nil)

	if len(cfg.TrustedProxies) != 0 {
		t.Errorf("TrustedProxies = %v, want empty — an operator who has not named "+
			"their proxies has not opted in to believing any header", cfg.TrustedProxies)
	}
	if want := auth.DefaultParams(); cfg.PasswordHashing != want {
		t.Errorf("PasswordHashing = %+v, want %+v", cfg.PasswordHashing, want)
	}
	limit, window := auth.LimiterDefaults()
	if cfg.LoginFailureLimit != limit || cfg.LoginFailureWindow != window {
		t.Errorf("login limits = %d/%v, want %d/%v",
			cfg.LoginFailureLimit, cfg.LoginFailureWindow, limit, window)
	}
	if cfg.SessionTTL != 30*24*time.Hour {
		t.Errorf("SessionTTL = %v, want 720h", cfg.SessionTTL)
	}
}

// 2.
func TestTrustedProxiesAcceptsUnambiguousForms(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		raw  string
		want []string
	}{
		"a single ipv4 network":    {"10.0.0.0/8", []string{"10.0.0.0/8"}},
		"a single ipv6 network":    {"2001:db8::/32", []string{"2001:db8::/32"}},
		"a bare ipv4 host":         {"192.168.1.5", []string{"192.168.1.5/32"}},
		"a bare ipv6 host":         {"2001:db8::1", []string{"2001:db8::1/128"}},
		"several, with spacing":    {" 10.0.0.0/8 , 192.168.0.0/16 ", []string{"10.0.0.0/8", "192.168.0.0/16"}},
		"mixed families":           {"10.0.0.0/8,2001:db8::/32", []string{"10.0.0.0/8", "2001:db8::/32"}},
		"overlapping is not a bug": {"10.0.0.0/8,10.1.0.0/16", []string{"10.0.0.0/8", "10.1.0.0/16"}},
		"a single host as /32":     {"192.168.1.5/32", []string{"192.168.1.5/32"}},
		"loopback":                 {"127.0.0.1", []string{"127.0.0.1/32"}},
		// A bare IPv4-in-IPv6 address has no prefix length to get wrong, so it
		// is accepted and normalised. The /104 form is refused — see below.
		"a bare ipv4-in-ipv6 host": {"::ffff:10.0.0.1", []string{"10.0.0.1/32"}},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			cfg := loadOK(t, map[string]string{"NUSA_TRUSTED_PROXIES": tc.raw})

			if len(cfg.TrustedProxies) != len(tc.want) {
				t.Fatalf("TrustedProxies = %v, want %v", cfg.TrustedProxies, tc.want)
			}
			for i, want := range tc.want {
				if got := cfg.TrustedProxies[i].String(); got != want {
					t.Errorf("TrustedProxies[%d] = %s, want %s", i, got, want)
				}
			}
			// Whatever the entry looked like, it must be usable as a prefix.
			for _, p := range cfg.TrustedProxies {
				if !p.IsValid() {
					t.Errorf("produced an invalid prefix from %q", tc.raw)
				}
			}
		})
	}
}

// 3. Every one of these has a reading the operator might have meant and a
// different reading the parser would pick. Refusing costs them one edit;
// guessing costs them a network they did not intend to trust.
func TestTrustedProxiesRefusesAmbiguousAndDangerousForms(t *testing.T) {
	t.Parallel()

	cases := map[string]struct{ raw, mustMention string }{
		"host bits set on an ipv4 range": {"10.0.0.1/24", "10.0.0.0/24"},
		"host bits set on an ipv6 range": {"2001:db8::1/32", "2001:db8::/32"},
		"trusts the whole internet":      {"0.0.0.0/0", "every client"},
		"trusts all of ipv6":             {"::/0", "every client"},
		"a trailing comma":               {"10.0.0.0/8,", "empty entry"},
		"a doubled comma":                {"10.0.0.0/8,,192.168.0.0/16", "empty entry"},
		"the same entry twice":           {"10.0.0.0/8,10.0.0.0/8", "more than once"},
		"the same host written twice":    {"10.0.0.1,10.0.0.1/32", "more than once"},
		"not an address at all":          {"nonsense", "neither an IP address nor a CIDR"},
		"a hostname":                     {"proxy.internal", "neither an IP address nor a CIDR"},
		"a range with a bad length":      {"10.0.0.0/99", "not a valid CIDR"},
		"an ipv4 network in ipv6 form":   {"::ffff:10.0.0.0/104", "IPv6 form"},
		"a zone on a bare address":       {"fe80::1%eth0", "zone"},
		"only whitespace":                {"   ,   ", "empty entry"},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			problem := loadProblem(t, map[string]string{"NUSA_TRUSTED_PROXIES": tc.raw})
			if !strings.Contains(problem, "NUSA_TRUSTED_PROXIES") {
				t.Errorf("problem %q does not name the variable", problem)
			}
			if !strings.Contains(problem, tc.mustMention) {
				t.Errorf("problem for %q was %q, which does not mention %q — "+
					"an operator has to be able to tell what to change",
					tc.raw, problem, tc.mustMention)
			}
		})
	}

	t.Run("a value of only whitespace is treated as unset", func(t *testing.T) {
		t.Parallel()
		// The shared get() helper trims and treats blank as absent, so this is
		// the no-proxies default rather than a rejection.
		cfg := loadOK(t, map[string]string{"NUSA_TRUSTED_PROXIES": "   "})
		if len(cfg.TrustedProxies) != 0 {
			t.Errorf("TrustedProxies = %v, want empty", cfg.TrustedProxies)
		}
	})
}

// 4.
func TestArgon2CostMayBeRaisedAndNeverLowered(t *testing.T) {
	t.Parallel()
	floor := auth.DefaultParams()

	t.Run("raising", func(t *testing.T) {
		t.Parallel()
		cfg := loadOK(t, map[string]string{
			"NUSA_ARGON2_MEMORY_KIB":  "65536",
			"NUSA_ARGON2_ITERATIONS":  "4",
			"NUSA_ARGON2_PARALLELISM": "2",
		})
		if cfg.PasswordHashing.Memory != 65536 ||
			cfg.PasswordHashing.Iterations != 4 ||
			cfg.PasswordHashing.Parallelism != 2 {
			t.Fatalf("PasswordHashing = %+v, want the raised values", cfg.PasswordHashing)
		}
		// Anything config produces must be something auth will actually accept,
		// or the instance mints credentials it cannot verify.
		if _, err := auth.NewHasher(cfg.PasswordHashing); err != nil {
			t.Fatalf("config produced parameters auth refuses: %v", err)
		}
	})

	t.Run("setting exactly the floor", func(t *testing.T) {
		t.Parallel()
		cfg := loadOK(t, map[string]string{
			"NUSA_ARGON2_MEMORY_KIB": "19456",
			"NUSA_ARGON2_ITERATIONS": "2",
		})
		if cfg.PasswordHashing != floor {
			t.Errorf("PasswordHashing = %+v, want %+v", cfg.PasswordHashing, floor)
		}
	})

	lowering := map[string]map[string]string{
		"memory below policy":     {"NUSA_ARGON2_MEMORY_KIB": "8192"},
		"memory at argon's floor": {"NUSA_ARGON2_MEMORY_KIB": "8"},
		"memory at zero":          {"NUSA_ARGON2_MEMORY_KIB": "0"},
		"iterations below policy": {"NUSA_ARGON2_ITERATIONS": "1"},
		"iterations at zero":      {"NUSA_ARGON2_ITERATIONS": "0"},
		"parallelism at zero":     {"NUSA_ARGON2_PARALLELISM": "0"},
	}
	for name, extra := range lowering {
		t.Run("refuses "+name, func(t *testing.T) {
			t.Parallel()
			problem := loadProblem(t, extra)
			if !strings.Contains(problem, "never lowered") {
				t.Errorf("problem was %q, want it to say the cost may never be lowered", problem)
			}
		})
	}

	ceilings := map[string]map[string]string{
		"memory beyond what auth will verify": {"NUSA_ARGON2_MEMORY_KIB": "2097152"},
		"absurd iterations":                   {"NUSA_ARGON2_ITERATIONS": "5000"},
		"absurd parallelism":                  {"NUSA_ARGON2_PARALLELISM": "512"},
	}
	for name, extra := range ceilings {
		t.Run("refuses "+name, func(t *testing.T) {
			t.Parallel()
			problem := loadProblem(t, extra)
			if !strings.Contains(problem, "exceeds the maximum") {
				t.Errorf("problem was %q, want it to name the maximum", problem)
			}
		})
	}

	t.Run("the memory ceiling is auth's own, not a copy", func(t *testing.T) {
		t.Parallel()
		// One below the ceiling is accepted; the ceiling itself is accepted;
		// one above is not. Anchoring on auth.MaxVerifiableMemory means the two
		// numbers cannot drift into a state where config mints hashes that
		// auth.Verify refuses to read.
		cfg := loadOK(t, map[string]string{
			"NUSA_ARGON2_MEMORY_KIB": itoa(auth.MaxVerifiableMemory),
		})
		if cfg.PasswordHashing.Memory != auth.MaxVerifiableMemory {
			t.Fatalf("Memory = %d, want %d", cfg.PasswordHashing.Memory, auth.MaxVerifiableMemory)
		}
		loadProblem(t, map[string]string{
			"NUSA_ARGON2_MEMORY_KIB": itoa(auth.MaxVerifiableMemory + 1),
		})
	})

	for name, extra := range map[string]map[string]string{
		"non-numeric memory": {"NUSA_ARGON2_MEMORY_KIB": "lots"},
		"negative memory":    {"NUSA_ARGON2_MEMORY_KIB": "-1"},
		"a float":            {"NUSA_ARGON2_ITERATIONS": "2.5"},
	} {
		t.Run("refuses "+name, func(t *testing.T) {
			t.Parallel()
			problem := loadProblem(t, extra)
			if !strings.Contains(problem, "not a whole number") {
				t.Errorf("problem was %q, want it to say the value is not a whole number", problem)
			}
		})
	}
}

func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	var b []byte
	for v > 0 {
		b = append([]byte{byte('0' + v%10)}, b...)
		v /= 10
	}
	return string(b)
}

// 5. Decision (c): in production the Secure attribute is on, and nothing in the
// environment can turn it off.
func TestSecureCookiesCannotBeDisabledInProduction(t *testing.T) {
	t.Parallel()

	t.Run("production", func(t *testing.T) {
		t.Parallel()
		cfg := loadOK(t, map[string]string{"NUSA_ENV": "production"})
		if !cfg.SecureCookies() {
			t.Fatal("SecureCookies() = false in production")
		}
	})

	t.Run("development, so localhost over http still works", func(t *testing.T) {
		t.Parallel()
		cfg := loadOK(t, map[string]string{"NUSA_ENV": "development"})
		if cfg.SecureCookies() {
			t.Fatal("SecureCookies() = true in development, which stops anyone signing in over http://localhost")
		}
	})

	// The part that matters. Every spelling somebody in a hurry might reach
	// for, set to every value that means "off", all at once. A flag that can be
	// turned off is a flag that gets turned off in production at three in the
	// morning to make a misconfigured proxy work, and the session cookie then
	// travels in clear.
	saboteurs := []string{
		"NUSA_COOKIE_SECURE", "NUSA_SECURE_COOKIES", "NUSA_COOKIES_SECURE",
		"NUSA_SECURE_COOKIE", "NUSA_INSECURE_COOKIES", "NUSA_ALLOW_INSECURE_COOKIES",
		"NUSA_DISABLE_SECURE_COOKIES", "NUSA_HTTPS", "NUSA_TLS", "NUSA_TLS_ENABLED",
		"NUSA_INSECURE", "NUSA_DEV", "NUSA_DEV_MODE", "NUSA_DEBUG",
	}
	for _, value := range []string{"false", "0", "no", "off", "", "true"} {
		t.Run("resists overrides set to "+quote(value), func(t *testing.T) {
			t.Parallel()
			extra := map[string]string{"NUSA_ENV": "production"}
			for _, name := range saboteurs {
				extra[name] = value
			}
			cfg, err := config.Load(withEnv(extra))
			if err != nil {
				// Unknown variables are ignored rather than rejected, so this
				// must load cleanly. If it ever does not, the assertion below
				// would never run and this test would be proving nothing.
				t.Fatalf("Load() = %v, want success", err)
			}
			if !cfg.SecureCookies() {
				t.Fatalf("SecureCookies() = false in production after setting %d override "+
					"variables to %q — one of them is being read", len(saboteurs), value)
			}
		})
	}
}

func quote(s string) string {
	if s == "" {
		return "an empty string"
	}
	return `"` + s + `"`
}

// A guard on the guard: netip.Prefix values that reach ClientIP must already be
// masked, because an unmasked prefix silently contains addresses the operator
// never named. config is where that is established.
func TestEveryProducedPrefixIsMasked(t *testing.T) {
	t.Parallel()
	cfg := loadOK(t, map[string]string{
		"NUSA_TRUSTED_PROXIES": "10.0.0.0/8,192.168.1.5,2001:db8::/32,::ffff:10.1.2.3",
	})
	if len(cfg.TrustedProxies) != 4 {
		t.Fatalf("got %d prefixes, want 4", len(cfg.TrustedProxies))
	}
	for _, p := range cfg.TrustedProxies {
		if p != p.Masked() {
			t.Errorf("%s is not masked", p)
		}
		if p.Addr().Is4In6() {
			t.Errorf("%s kept its IPv4-in-IPv6 form, which matches no unmapped request address", p)
		}
		if p.Addr().Zone() != "" {
			t.Errorf("%s kept a zone", p)
		}
	}
	if got := cfg.TrustedProxies[3].String(); got != "10.1.2.3/32" {
		t.Errorf("bare ipv4-in-ipv6 normalised to %s, want 10.1.2.3/32", got)
	}
	_ = netip.Prefix{}
}
