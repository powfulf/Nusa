// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"net/http"
	"time"

	"github.com/powfulf/Nusa/internal/brand"
)

// The session cookie.
//
// §10 fixes three of its attributes and this file adds a fourth. HttpOnly, so
// script cannot read the token — the single most effective thing against a
// cross-site scripting bug turning into a stolen session. SameSite=Lax, so the
// cookie does not accompany a cross-site POST, which is most of CSRF removed
// without a token scheme. Secure, whenever the deployment is production, and
// not switchable — see Config.SecureCookies. Path=/, because the whole
// application is behind it.
//
// There is no Domain attribute, deliberately. Omitting it produces a
// host-only cookie, which is narrower: with a Domain the cookie would also be
// sent to every subdomain, and a subdomain is exactly the thing an attacker is
// most likely to control on somebody's self-hosted box.

// sessionCookieName returns the cookie name for a deployment.
//
// In production the name carries the __Host- prefix, which is a promise the
// browser enforces rather than a convention: a cookie so named is refused
// unless it is Secure, has Path=/, and has no Domain. What that buys is
// protection against a *subdomain* setting a session cookie for the parent —
// an attacker on blog.example.org cannot overwrite a __Host- cookie belonging
// to nusa.example.org, and without the prefix they can.
//
// Development cannot use it, because the prefix requires Secure and Secure is
// off there so that http://localhost works at all. The name therefore differs
// between environments, which means switching NUSA_ENV invalidates existing
// cookies. That is the correct outcome: a session minted under one set of
// transport guarantees should not silently carry over into another.
func sessionCookieName(secure bool) string {
	if secure {
		return "__Host-" + brand.Slug + "_session"
	}
	return brand.Slug + "_session"
}

// setSessionCookie writes the token that identifies a session.
//
// gosec's G124 wants Secure set to a literal true and is suppressed rather than
// obeyed. Secure is not a constant here on purpose: it comes from
// Config.SecureCookies, which is true in production and cannot be turned off by
// any variable, and false in development because a Secure cookie is not sent
// over http://localhost by every browser — so obeying the linter would mean
// nobody could sign in while working on the frontend. HttpOnly and SameSite
// *are* literals, which is the part of the rule that should be unconditional.
func setSessionCookie(w http.ResponseWriter, token string, expires time.Time, secure bool) {
	//nolint:gosec // G124: Secure is derived from Config.SecureCookies, see above
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName(secure),
		Value:    token,
		Path:     "/",
		Expires:  expires,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	})
}

// clearSessionCookie removes the cookie from the browser.
//
// It is never the whole of signing out. A cookie is deleted on one device; the
// session it named lives on the server and would still be accepted from
// anywhere else the token had reached. Revocation is what ends a session, and
// this only tidies up afterwards — see handleLogout.
func clearSessionCookie(w http.ResponseWriter, secure bool) {
	//nolint:gosec // G124: same as setSessionCookie, and this one carries no value at all
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName(secure),
		Value:    "",
		Path:     "/",
		Expires:  time.Unix(0, 0).UTC(),
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	})
}

// sessionToken reads the token a request presents, if any.
func sessionToken(r *http.Request, secure bool) string {
	cookie, err := r.Cookie(sessionCookieName(secure))
	if err != nil {
		return ""
	}
	return cookie.Value
}
