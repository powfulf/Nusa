// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// Resolving the address of whoever is making a request.
//
// This is a security guard rather than a convenience. Everything downstream
// that treats an address as an identity — the login rate limiter first, an
// audit trail later — is only as trustworthy as this function, because an
// address a client can choose is an address that identifies nobody.
//
// §10 of CLAUDE.md states the rule: no middleware trusts X-Forwarded-For,
// X-Real-IP or True-Client-IP, and anything needing a client address takes an
// explicit list of trusted proxies from configuration. chi's RealIP middleware
// was removed during M0 for exactly this — it believes all three headers
// unconditionally, which lets a client forge its own address and defeat
// precisely the protection the address was wanted for.
//
// Only X-Forwarded-For is read here, and only from a peer already known to be
// a proxy. X-Real-IP and True-Client-IP are ignored outright and there is no
// option to enable them: they carry a single value with no chain, so there is
// no way to tell which hop set them and no way to verify anything about them.
// A header that cannot be verified is not a weaker signal, it is not a signal.

// maxForwardedHops bounds how far into X-Forwarded-For this will look.
//
// The header is attacker-supplied in length as well as content: a client behind
// a trusted proxy can send a header with a hundred thousand entries, and the
// proxy will append to it rather than replace it. Walking all of them is work
// an attacker chose for us.
//
// Fifty is far past any real deployment — a request crossing more than a
// handful of proxies is already unusual — and the walk starts from the right,
// so a header of any size costs at most the rightmost fifty fields. Reaching
// the cap is not an error: it stops, and answers with the last address it could
// actually vouch for.
const maxForwardedHops = 50

// ClientIP returns the address to attribute a request to.
//
// The returned address is invalid (`!addr.IsValid()`) only when RemoteAddr
// itself cannot be parsed, which does not happen for a request that arrived
// over TCP. Callers must still handle it rather than assume.
//
// The rules, in the order they apply:
//
//  1. The peer address from RemoteAddr is the only thing known for certain. It
//     is what the connection actually came from and cannot be forged.
//  2. If trusted is empty, that peer is the answer and no header is read at
//     all. An operator who has not named their proxies has not opted in to
//     believing anything, and a half-trusted header is worse than an ignored
//     one because it looks like it was checked.
//  3. If the peer is not in trusted, that peer is the answer. A client that is
//     not a proxy we run has nothing to tell us about who it is.
//  4. Otherwise the peer is one of our proxies, and X-Forwarded-For is walked
//     from the right. Each hop that is itself trusted is stripped; the first
//     hop that is not is the client, and everything further left is ignored —
//     it was written by something we do not control.
func ClientIP(r *http.Request, trusted []netip.Prefix) netip.Addr {
	peer, ok := peerAddr(r.RemoteAddr)
	if !ok {
		return netip.Addr{}
	}
	if len(trusted) == 0 || !inAny(peer, trusted) {
		return peer
	}

	// verified is the address furthest to the left that this has been able to
	// vouch for. It starts as the peer and moves left one trusted hop at a
	// time, so whatever is returned is always an address some trusted party
	// actually observed.
	verified := peer
	hops := 0

	// A header may arrive as several lines; semantically they join in order,
	// so the last field of the last line is the rightmost hop.
	values := r.Header.Values("X-Forwarded-For")
	for i := len(values) - 1; i >= 0; i-- {
		rest := values[i]
		for rest != "" {
			if hops >= maxForwardedHops {
				return verified
			}
			hops++

			var field string
			if cut := strings.LastIndexByte(rest, ','); cut >= 0 {
				field, rest = rest[cut+1:], rest[:cut]
			} else {
				field, rest = rest, ""
			}

			addr, ok := forwardedAddr(field)
			if !ok {
				// Unparseable, obfuscated (RFC 7239 permits "unknown" and
				// "_hidden"), or simply junk. Nothing to the left of it can be
				// verified any more, so the walk stops here and answers with
				// the last hop it could vouch for rather than guessing.
				return verified
			}
			if !inAny(addr, trusted) {
				return addr
			}
			verified = addr
		}
	}
	return verified
}

// peerAddr parses the host part of a RemoteAddr.
func peerAddr(remoteAddr string) (netip.Addr, bool) {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		// A RemoteAddr without a port is not what net/http produces over TCP,
		// but it is what some test servers and unix sockets produce.
		host = remoteAddr
	}
	return parseAddr(host)
}

// forwardedAddr parses one X-Forwarded-For field.
//
// Proxies are inconsistent about this: some write a bare address, some append a
// port, some bracket IPv6 and some do not. All of those are accepted because
// all of them occur; anything else is refused rather than repaired.
func forwardedAddr(field string) (netip.Addr, bool) {
	field = strings.TrimSpace(field)
	if field == "" {
		return netip.Addr{}, false
	}

	// A bracketed form always carries a host, with or without a port.
	if strings.HasPrefix(field, "[") {
		if host, _, err := net.SplitHostPort(field); err == nil {
			return parseAddr(host)
		}
		return parseAddr(strings.Trim(field, "[]"))
	}

	// An unbracketed field with exactly one colon is host:port; more than one
	// colon is a bare IPv6 address, which must not be split.
	if strings.Count(field, ":") == 1 {
		if host, _, err := net.SplitHostPort(field); err == nil {
			return parseAddr(host)
		}
	}
	return parseAddr(field)
}

// parseAddr normalises an address for comparison against a prefix.
//
// Unmap matters: a client arriving over a dual-stack listener appears as
// ::ffff:10.0.0.1, and netip.Prefix.Contains is family-strict, so an IPv4
// prefix would not match it. Without this a trusted proxy would silently stop
// being recognised the day the listener changed. The zone is dropped for the
// same reason — a link-local address carrying %eth0 matches no prefix.
func parseAddr(s string) (netip.Addr, bool) {
	addr, err := netip.ParseAddr(strings.TrimSpace(s))
	if err != nil {
		return netip.Addr{}, false
	}
	return addr.Unmap().WithZone(""), true
}

func inAny(addr netip.Addr, trusted []netip.Prefix) bool {
	for _, prefix := range trusted {
		if prefix.Contains(addr) {
			return true
		}
	}
	return false
}
