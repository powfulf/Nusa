// SPDX-License-Identifier: AGPL-3.0-only

package api

import "net/http"

// Test-only handles on unexported middleware.
//
// The middleware is exercised directly rather than only through the router
// because one property cannot be reached from a route: what happens when a
// session is revoked while a handler is already running. Proving that needs a
// handler the test can hold open, and the routes are all real endpoints.
var (
	RequireSessionForTest       func(Deps) func(http.Handler) http.Handler = requireSession
	RequireAuthenticatedForTest func(Deps) func(http.Handler) http.Handler = requireAuthenticated
)
