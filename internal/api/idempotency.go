// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
)

// Idempotency over HTTP, on top of the guarantee internal/store already makes.
//
// §5.6 is a rule about writes, not about requests, and the repository enforces
// it: a claim is taken inside the write's own database transaction, so a
// second identical write is recognised and does nothing. What that leaves
// undone is the half a caller actually sees. A replay that writes nothing and
// then answers 200 with a freshly rendered body has not replayed a 201, and a
// client that reads the status to decide whether it created something is
// told the wrong thing by a layer that was working correctly underneath.
//
// So this layer stores what the first attempt answered and repeats it
// verbatim. It replaces nothing in the store; it finishes the promise at the
// only place where a status line exists.

// idempotencyKeyHeader is the header every mutation must carry.
const idempotencyKeyHeader = "Idempotency-Key"

// maxIdempotencyKeyLength bounds what will be stored and logged. The value is
// opaque to us, so the only honest limit is one generous enough for any
// identifier a client might reasonably mint — a UUID is 36 — and small enough
// that it cannot be used to write a large row per request.
const maxIdempotencyKeyLength = 255

// IdempotencyStore is the part of the repository this layer needs.
//
// It is declared here, in terms of plain types, so that internal/api does not
// import internal/store to name a parameter. The dependency runs the other
// way: the store satisfies this without knowing the interface exists.
type IdempotencyStore interface {
	// IdempotentResponse reads what an earlier attempt answered. The boolean
	// is false when there is no claim, or a claim with no response recorded
	// against it yet.
	IdempotentResponse(ctx context.Context, actorID, key string) (status int, body []byte, ok bool, err error)

	// SaveIdempotentResponse records what this attempt answered.
	SaveIdempotentResponse(ctx context.Context, actorID, key string, status int, body []byte) error
}

// LedgerDeps carries what the ledger endpoints need.
type LedgerDeps struct {
	// Idempotency records and replays mutation responses. Required.
	Idempotency IdempotencyStore

	// Journal reads the book. Required.
	Journal JournalReader
}

// ready reports whether the ledger routes can be mounted at all.
//
// An incomplete set means the routes are not mounted, which is a louder
// failure than mounting them and dereferencing nil on somebody's first read.
func (l *LedgerDeps) ready() bool {
	return l != nil && l.Idempotency != nil && l.Journal != nil
}

// idempotencyKeyFrom returns the key requireIdempotencyKey validated.
//
// Like sessionFrom, it reports false anywhere the middleware did not run,
// which is a programming error rather than a client one.
func idempotencyKeyFrom(ctx context.Context) (string, bool) {
	key, ok := ctx.Value(idempotencyKeyContextKey).(string)
	return key, ok
}

// requireIdempotencyKey refuses a mutation that carries no usable key.
//
// The key is never minted here when one is absent. Minting one would produce
// an endpoint that looks idempotent while every retry writes again — worse
// than an endpoint that is honestly not idempotent, because nothing in the
// response says which of the two the caller is holding. store.Write requires a
// key already; this is the edge agreeing with it rather than working around
// it.
func requireIdempotencyKey(d Deps) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			values := r.Header.Values(idempotencyKeyHeader)
			if len(values) == 0 {
				writeErrorDetail(w, d.Logger, http.StatusBadRequest, errorDetail{
					Code:    CodeIdempotencyKeyRequired,
					Message: "every mutation requires an " + idempotencyKeyHeader + " header",
					Field:   idempotencyKeyHeader,
				})
				return
			}
			// Two headers are two different claims about what this request is,
			// and picking one would be choosing on the client's behalf which
			// of its retries this counts as.
			if len(values) > 1 {
				writeErrorDetail(w, d.Logger, http.StatusBadRequest, errorDetail{
					Code:    CodeInvalidRequest,
					Message: "more than one " + idempotencyKeyHeader + " header",
					Field:   idempotencyKeyHeader,
				})
				return
			}
			if !usableIdempotencyKey(values[0]) {
				writeErrorDetail(w, d.Logger, http.StatusBadRequest, errorDetail{
					Code:    CodeInvalidRequest,
					Message: "the " + idempotencyKeyHeader + " header must be 1 to 255 printable ASCII characters",
					Field:   idempotencyKeyHeader,
				})
				return
			}

			ctx := context.WithValue(r.Context(), idempotencyKeyContextKey, values[0])
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// usableIdempotencyKey reports whether a key can be stored, compared and
// logged without any of those three changing it.
//
// The key is opaque and is never trimmed or normalised: it has to match
// byte-for-byte on the retry, and folding " k" into "k" would decide on the
// client's behalf that two different keys are one. Printable ASCII is what
// survives a header, a text column and a log line unaltered — and it excludes
// NUL, which the store would refuse anyway with an error naming neither the
// field nor the fix.
func usableIdempotencyKey(key string) bool {
	if key == "" || len(key) > maxIdempotencyKeyLength {
		return false
	}
	for i := 0; i < len(key); i++ {
		if key[i] < 0x20 || key[i] > 0x7E {
			return false
		}
	}
	return true
}

// writeIdempotentResponse answers a mutation and makes a replay of it answer
// the same way.
//
// The ordering is deliberate: the response is recorded before it is written.
// A crash between the two then leaves a claim whose answer is already stored,
// which the retry replays correctly. Recording afterwards would leave the
// opposite — a committed write whose answer nobody kept — and the caller would
// have no way to learn what happened.
//
// A failure to record is logged and does not fail the request. The write it
// describes has already committed, and answering 500 would tell the caller
// their mutation failed when it did not.
func writeIdempotentResponse(
	w http.ResponseWriter, r *http.Request, d Deps,
	actorID string, replayed bool, status int, body any,
) {
	key, ok := idempotencyKeyFrom(r.Context())
	if !ok {
		writeInternalError(w, d.Logger, "idempotent response outside requireIdempotencyKey",
			errNoIdempotencyKey)
		return
	}

	fresh, err := json.Marshal(body)
	if err != nil {
		writeInternalError(w, d.Logger, "encode mutation response", err)
		return
	}

	if replayed {
		storedStatus, storedBody, found, err := d.Ledger.Idempotency.IdempotentResponse(
			r.Context(), actorID, key)
		if err != nil {
			writeInternalError(w, d.Logger, "read idempotent response", err)
			return
		}
		if found {
			writeRawJSON(w, storedStatus, storedBody)
			return
		}
		// A claim with no recorded answer. The claim is taken inside the
		// write's transaction, so this means the write committed and the
		// process stopped before the answer was stored. Re-deriving it is
		// correct rather than approximate for an immutable entity, which is
		// every transaction; for a mutable one it reports the current state
		// rather than the original answer, and that is the honest limit of
		// what is recoverable here.
		d.Logger.Warn("idempotent claim carried no recorded response, re-deriving",
			slog.String("actor_id", actorID))
	}

	if err := d.Ledger.Idempotency.SaveIdempotentResponse(
		r.Context(), actorID, key, status, fresh); err != nil {
		d.Logger.Error("record idempotent response",
			slog.String("error", err.Error()),
			slog.String("actor_id", actorID))
	}
	writeRawJSON(w, status, fresh)
}

// writeRawJSON sends a document that is already encoded.
//
// A replay must repeat what the first attempt sent, so the stored bytes go out
// as they came in. Decoding and re-encoding them would let a change in field
// order, escaping or numeric formatting alter a response that is supposed to
// be identical.
func writeRawJSON(w http.ResponseWriter, status int, body []byte) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

// writeIdempotencyConflict answers a key that was claimed for a different
// request.
//
// This is a client bug rather than a race, and it is reported as one: the
// caller believes it is retrying something it is not, and replaying the first
// answer would tell it a write succeeded that was never attempted.
func writeIdempotencyConflict(w http.ResponseWriter, d Deps) {
	writeErrorDetail(w, d.Logger, http.StatusConflict, errorDetail{
		Code:    CodeIdempotencyKeyReused,
		Message: "this " + idempotencyKeyHeader + " was already used for a different request",
		Field:   idempotencyKeyHeader,
	})
}
