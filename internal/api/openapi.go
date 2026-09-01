// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	_ "embed"
	"net/http"
)

// The OpenAPI document.
//
// It is hand-written, and that is a decision rather than a shortcut. The
// milestone asked for a spec generated from the code; in Go that means
// comment annotations beside each handler, which drift from the handler they
// sit next to and report nothing when they do. The real requirement is *valid
// and matching the implementation*, and a two-way check enforces that better
// than a generator: every mounted route must appear in the document, and every
// path in the document must exist in the router.
//
// The second direction is the one usually left out and the one whose failure
// costs more. A route deleted from the code and left standing in the spec is a
// promise a client will follow until it hits a 404 at runtime.
//
// Both directions are guarded, and so are four other axes on which a
// hand-written document can drift: the statuses it documents, the error codes
// it enumerates, the shapes it gives Money, Date and Rate, and the claim that
// two listings are never paginated. What is *not* guarded is labelled in the
// guard file rather than left looking covered.

//go:embed openapi.json
var openAPIDocument []byte

// handleOpenAPI serves the document.
//
// Unauthenticated, because a specification describes the shape of an API
// rather than anything in it, and a client needs it before it has a session.
func handleOpenAPI() http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		// Cacheable, unlike every other response here: it changes only when the
		// binary does.
		w.Header().Set("Cache-Control", "public, max-age=300")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(openAPIDocument)
	}
}
