// SPDX-License-Identifier: AGPL-3.0-only

package api_test

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/GaffaQ/Nusa/internal/api"
)

// What these guards must cover. Written before they were, and each item was
// broken on purpose and watched failing afterwards, against a prediction
// written first (CLAUDE.md §11).
//
//	 1. A forged header from an untrusted client does not move the address.
//	 2. An empty trusted list means the header is ignored entirely, not
//	    partly believed.
//	 3. Stripping runs right to left, stops at the first untrusted hop, and
//	    ignores everything further left.
//	 4. A very long chain costs a bounded amount of work and cannot steer the
//	    answer from beyond the cap.
//	 5. A chain of only trusted hops answers with the leftmost of them.
//	 6. An unparseable entry stops the walk at the last verified hop.
//	 7. Several header lines join in order; the rightmost field is the last hop.
//	 8. Ports, brackets, IPv6 and IPv4-in-IPv6 all parse.
//	 9. X-Real-IP and True-Client-IP are never read, from any peer.
//	10. An unparseable RemoteAddr yields an invalid address rather than a guess.

func prefixes(t *testing.T, entries ...string) []netip.Prefix {
	t.Helper()
	out := make([]netip.Prefix, 0, len(entries))
	for _, e := range entries {
		p, err := netip.ParsePrefix(e)
		if err != nil {
			t.Fatalf("bad test prefix %q: %v", e, err)
		}
		out = append(out, p)
	}
	return out
}

// request builds a request from a peer address and any headers.
func request(remoteAddr string, headers map[string][]string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/api/v1/sessions", nil)
	r.RemoteAddr = remoteAddr
	for name, values := range headers {
		for _, v := range values {
			r.Header.Add(name, v)
		}
	}
	return r
}

func xff(values ...string) map[string][]string {
	return map[string][]string{"X-Forwarded-For": values}
}

func mustAddr(t *testing.T, s string) netip.Addr {
	t.Helper()
	a, err := netip.ParseAddr(s)
	if err != nil {
		t.Fatalf("bad test address %q: %v", s, err)
	}
	return a
}

// 1, 2. The two cases the whole guard exists for.
func TestAForgedHeaderCannotMoveTheAddress(t *testing.T) {
	t.Parallel()

	// What an attacker sends when they want to be somebody else: a plausible
	// header, from a machine that is not one of our proxies.
	forged := xff("10.0.0.9, 203.0.113.7", "198.51.100.1")

	t.Run("no trusted proxies configured", func(t *testing.T) {
		t.Parallel()
		// Requirement: an empty list means the header is ignored *entirely*.
		// Not "believed for the last hop", not "used if it looks internal".
		got := api.ClientIP(request("203.0.113.50:44321", forged), nil)
		if want := mustAddr(t, "203.0.113.50"); got != want {
			t.Fatalf("ClientIP = %v, want the peer %v — an unconfigured list must ignore the header", got, want)
		}

		// An empty non-nil slice must behave identically to nil.
		got = api.ClientIP(request("203.0.113.50:44321", forged), []netip.Prefix{})
		if want := mustAddr(t, "203.0.113.50"); got != want {
			t.Fatalf("ClientIP = %v, want %v", got, want)
		}
	})

	t.Run("peer is not a trusted proxy", func(t *testing.T) {
		t.Parallel()
		trusted := prefixes(t, "10.0.0.0/8")
		got := api.ClientIP(request("203.0.113.50:44321", forged), trusted)
		if want := mustAddr(t, "203.0.113.50"); got != want {
			t.Fatalf("ClientIP = %v, want the peer %v — a client that is not a proxy tells us nothing", got, want)
		}
	})

	t.Run("the client prepends a forgery and the proxy appends the truth", func(t *testing.T) {
		t.Parallel()
		// This is the attack in its real shape. A proxy *appends* to
		// X-Forwarded-For rather than replacing it, so whatever the client
		// sent survives to the left of the address the proxy observed.
		// Reading left to right — which is the naive implementation — hands
		// the attacker the answer.
		trusted := prefixes(t, "10.0.0.0/8")
		r := request("10.0.0.1:9000", xff("1.2.3.4, 203.0.113.77"))

		got := api.ClientIP(r, trusted)
		if want := mustAddr(t, "203.0.113.77"); got != want {
			t.Fatalf("ClientIP = %v, want %v — the rightmost untrusted hop is the client, "+
				"everything left of it was written by the client itself", got, want)
		}
	})
}

// 3, 5.
func TestStrippingRunsRightToLeftAndStopsAtTheFirstUntrustedHop(t *testing.T) {
	t.Parallel()
	trusted := prefixes(t, "10.0.0.0/8", "192.168.0.0/16")

	cases := map[string]struct {
		peer   string
		header []string
		want   string
		why    string
	}{
		"one proxy": {
			peer:   "10.0.0.1:9000",
			header: []string{"203.0.113.77"},
			want:   "203.0.113.77",
		},
		"two proxies, both trusted": {
			peer:   "10.0.0.1:9000",
			header: []string{"203.0.113.77, 192.168.1.1"},
			want:   "203.0.113.77",
			why:    "192.168.1.1 is ours and is stripped; the next one left is the client",
		},
		"forged entries beyond the first untrusted hop are ignored": {
			peer:   "10.0.0.1:9000",
			header: []string{"9.9.9.9, 8.8.8.8, 203.0.113.77, 192.168.1.1"},
			want:   "203.0.113.77",
			why:    "everything left of the first untrusted hop came from the client",
		},
		"every hop trusted": {
			peer:   "10.0.0.1:9000",
			header: []string{"10.5.5.5, 192.168.1.1"},
			want:   "10.5.5.5",
			why:    "no client address was ever recorded; the leftmost we can vouch for is the answer",
		},
		"no header at all": {
			peer:   "10.0.0.1:9000",
			header: nil,
			want:   "10.0.0.1",
			why:    "a trusted peer that forwarded nothing is itself the client",
		},
		"empty header value": {
			peer:   "10.0.0.1:9000",
			header: []string{""},
			want:   "10.0.0.1",
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got := api.ClientIP(request(tc.peer, xff(tc.header...)), trusted)
			if want := mustAddr(t, tc.want); got != want {
				t.Fatalf("ClientIP = %v, want %v. %s", got, want, tc.why)
			}
		})
	}
}

// 4. A very long chain is bounded, and — the part that matters — cannot decide
// the answer from beyond the cap.
//
// The assertion is on the value rather than on a stopwatch. The header below
// puts an untrusted address at the far left, past 200 trusted hops: an
// implementation that walks the whole chain finds it and returns it, and one
// that stops at maxForwardedHops never sees it. So a timing measurement is not
// needed to tell the two apart, which is just as well, because a timing
// assertion would be the flakiest thing in this file.
func TestAVeryLongChainIsBoundedAndCannotSteerTheAnswer(t *testing.T) {
	t.Parallel()
	trusted := prefixes(t, "10.0.0.0/8")

	const hops = 200
	chain := make([]string, 0, hops+1)
	chain = append(chain, "203.0.113.99") // the bait, far to the left
	for range hops {
		chain = append(chain, "10.0.0.2")
	}
	header := strings.Join(chain, ", ")

	start := time.Now()
	got := api.ClientIP(request("10.0.0.1:9000", xff(header)), trusted)
	elapsed := time.Since(start)

	if want := mustAddr(t, "10.0.0.2"); got != want {
		t.Fatalf("ClientIP = %v, want %v — the walk reached past the hop cap and "+
			"took an address the client planted there", got, want)
	}
	t.Logf("200-hop chain resolved in %v", elapsed)

	// The same shape at a size no legitimate deployment produces. The point is
	// that the cost is set by the cap and not by the header.
	huge := strings.Repeat("10.0.0.2, ", 100_000) + "10.0.0.3"
	start = time.Now()
	got = api.ClientIP(request("10.0.0.1:9000", xff(huge)), trusted)
	elapsed = time.Since(start)
	if !got.IsValid() {
		t.Fatalf("ClientIP returned an invalid address for a long chain")
	}
	t.Logf("100k-hop chain resolved in %v (result %v)", elapsed, got)
	if elapsed > 2*time.Second {
		t.Fatalf("resolving a long chain took %v, which means the walk is not bounded", elapsed)
	}
}

// 6.
func TestAnUnparseableEntryStopsTheWalk(t *testing.T) {
	t.Parallel()
	trusted := prefixes(t, "10.0.0.0/8")

	cases := map[string]struct{ header, want, why string }{
		"obfuscated per RFC 7239": {
			header: "_hidden, 10.0.0.2",
			want:   "10.0.0.2",
			why:    "the last hop we could vouch for, not the obfuscated token",
		},
		"unknown": {
			header: "unknown, 10.0.0.2",
			want:   "10.0.0.2",
		},
		"junk": {
			header: "not-an-address, 10.0.0.2",
			want:   "10.0.0.2",
		},
		"junk at the rightmost position": {
			header: "203.0.113.5, garbage",
			want:   "10.0.0.1",
			why:    "nothing in the header could be verified, so the peer stands",
		},
		"a hostname rather than an address": {
			header: "proxy.internal, 10.0.0.2",
			want:   "10.0.0.2",
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got := api.ClientIP(request("10.0.0.1:9000", xff(tc.header)), trusted)
			if want := mustAddr(t, tc.want); got != want {
				t.Fatalf("ClientIP = %v, want %v. %s", got, want, tc.why)
			}
		})
	}
}

// 7, 8.
func TestHeaderLinesJoinAndAddressFormsParse(t *testing.T) {
	t.Parallel()
	trusted := prefixes(t, "10.0.0.0/8", "2001:db8::/32")

	t.Run("several header lines join in order", func(t *testing.T) {
		t.Parallel()
		// Two lines: the rightmost field of the last line is the newest hop.
		r := request("10.0.0.1:9000", xff("203.0.113.77", "10.0.0.2"))
		got := api.ClientIP(r, trusted)
		if want := mustAddr(t, "203.0.113.77"); got != want {
			t.Fatalf("ClientIP = %v, want %v", got, want)
		}
	})

	t.Run("a forgery on an earlier line does not win", func(t *testing.T) {
		t.Parallel()
		// The case above cannot tell the two orders apart: its untrusted
		// address is leftmost either way, so reading the lines forwards
		// produces the same answer and the guard proves nothing about order.
		// Established by reversing the loop and watching it stay green.
		//
		// Here the client planted an address on the first line and the proxy
		// appended the truth to the last. Reading forwards returns the
		// forgery; reading backwards returns what the proxy observed.
		r := request("10.0.0.1:9000", xff("1.2.3.4", "203.0.113.77, 10.0.0.2"))
		got := api.ClientIP(r, trusted)
		if want := mustAddr(t, "203.0.113.77"); got != want {
			t.Fatalf("ClientIP = %v, want %v — an address on an earlier header line "+
				"is further from the server and must not outrank a later one", got, want)
		}
	})

	forms := map[string]struct{ header, want string }{
		"bare ipv4":              {"203.0.113.77", "203.0.113.77"},
		"ipv4 with a port":       {"203.0.113.77:51234", "203.0.113.77"},
		"bare ipv6":              {"2001:db9::1", "2001:db9::1"},
		"bracketed ipv6":         {"[2001:db9::1]", "2001:db9::1"},
		"bracketed ipv6 witport": {"[2001:db9::1]:51234", "2001:db9::1"},
		"ipv4 in ipv6":           {"::ffff:203.0.113.77", "203.0.113.77"},
		"surrounded by spaces":   {"   203.0.113.77   ", "203.0.113.77"},
	}
	for name, tc := range forms {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got := api.ClientIP(request("10.0.0.1:9000", xff(tc.header)), trusted)
			if want := mustAddr(t, tc.want); got != want {
				t.Fatalf("ClientIP = %v, want %v", got, want)
			}
		})
	}

	t.Run("a trusted ipv6 proxy is recognised and stripped", func(t *testing.T) {
		t.Parallel()
		r := request("[2001:db8::1]:9000", xff("203.0.113.77, 2001:db8::2"))
		got := api.ClientIP(r, trusted)
		if want := mustAddr(t, "203.0.113.77"); got != want {
			t.Fatalf("ClientIP = %v, want %v", got, want)
		}
	})

	t.Run("a peer arriving as ipv4-in-ipv6 still matches an ipv4 prefix", func(t *testing.T) {
		t.Parallel()
		// A dual-stack listener reports ::ffff:10.0.0.1. If that stopped
		// matching 10.0.0.0/8 the proxy would silently become untrusted and
		// every request would be attributed to the proxy instead of the client.
		r := request("[::ffff:10.0.0.1]:9000", xff("203.0.113.77"))
		got := api.ClientIP(r, trusted)
		if want := mustAddr(t, "203.0.113.77"); got != want {
			t.Fatalf("ClientIP = %v, want %v — the proxy was not recognised through its mapped form", got, want)
		}
	})
}

// 9. The headers §10 names, which must never be consulted.
func TestSingleValuedForwardingHeadersAreNeverRead(t *testing.T) {
	t.Parallel()
	trusted := prefixes(t, "10.0.0.0/8")

	for _, header := range []string{"X-Real-IP", "True-Client-IP", "CF-Connecting-IP", "Forwarded"} {
		t.Run(header, func(t *testing.T) {
			t.Parallel()
			// Sent from a *trusted* peer, which is the most favourable case
			// these headers could have. They still must not be read: a single
			// value carries no chain, so there is no way to tell which hop set
			// it and nothing about it can be verified.
			r := request("10.0.0.1:9000", map[string][]string{header: {"203.0.113.77"}})
			got := api.ClientIP(r, trusted)
			if want := mustAddr(t, "10.0.0.1"); got != want {
				t.Fatalf("ClientIP = %v, want the peer %v — %s must not be consulted", got, want, header)
			}
		})
	}
}

// 10.
func TestAnUnparseablePeerYieldsAnInvalidAddress(t *testing.T) {
	t.Parallel()
	trusted := prefixes(t, "10.0.0.0/8")

	for name, remote := range map[string]string{
		"empty":            "",
		"not an address":   "pipe",
		"port only":        ":9000",
		"unix socket path": "@",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got := api.ClientIP(request(remote, xff("203.0.113.77")), trusted)
			if got.IsValid() {
				t.Fatalf("ClientIP = %v, want an invalid address — a peer that cannot be "+
					"parsed must not fall back to a header", got)
			}
		})
	}

	t.Run("an address without a port still parses", func(t *testing.T) {
		t.Parallel()
		got := api.ClientIP(request("10.0.0.1", nil), trusted)
		if want := mustAddr(t, "10.0.0.1"); got != want {
			t.Fatalf("ClientIP = %v, want %v", got, want)
		}
	})
}
