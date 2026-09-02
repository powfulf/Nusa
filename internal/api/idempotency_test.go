// SPDX-License-Identifier: AGPL-3.0-only

package api_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/powfulf/Nusa/internal/api"
)

// What these guards must cover, decided before any of them was written.
//
// The list is here rather than inferred from the assertions afterwards,
// because a guard audited against itself is audited against the thing it
// forgot. Each item was broken on purpose and watched failing.
//
//  1. A mutation with no key is refused, and the handler never runs.
//  2. Two keys are refused: picking one would choose, on the client's behalf,
//     which of its retries this counts as.
//  3. An unusable key is refused — empty, over-length, control character,
//     non-ASCII — and the handler never runs.
//  4. A usable key reaches the handler exactly as sent, with no trimming.
//  5. A first attempt records the status and the exact bytes it answered with.
//  6. A replay answers with the recorded status, not the fresh one.
//  7. A replay answers with the recorded bytes, not a re-encoding of them.
//  8. A replay whose claim carries no recorded response answers from the fresh
//     rendering rather than failing, and repairs the record.
//  9. A failure to record does not fail a request whose write has committed.
//  10. A failure to read a recorded response is not answered with a guess.
//  11. A handler reached without the middleware reports an internal error
//     rather than panicking.
//  12. A key reused for a different request is refused with its own code.

// fakeIdempotency stands in for the repository's two response methods.
//
// It is a fake rather than a mock: it stores what it is given and hands it
// back. The real implementation is covered against PostgreSQL in
// internal/store, which is where the schema's own guarantees are proved.
type fakeIdempotency struct {
	mu sync.Mutex

	status map[string]int
	body   map[string][]byte

	saves int
	reads int

	failSave error
	failRead error
}

func newFakeIdempotency() *fakeIdempotency {
	return &fakeIdempotency{status: map[string]int{}, body: map[string][]byte{}}
}

func (f *fakeIdempotency) IdempotentResponse(
	_ context.Context, actorID, key string,
) (int, []byte, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reads++
	if f.failRead != nil {
		return 0, nil, false, f.failRead
	}
	at := actorID + "\x00" + key
	status, ok := f.status[at]
	if !ok {
		return 0, nil, false, nil
	}
	return status, f.body[at], true, nil
}

func (f *fakeIdempotency) SaveIdempotentResponse(
	_ context.Context, actorID, key string, status int, body []byte,
) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.saves++
	if f.failSave != nil {
		return f.failSave
	}
	at := actorID + "\x00" + key
	f.status[at] = status
	f.body[at] = body
	return nil
}

func (f *fakeIdempotency) recorded(actorID, key string) (int, []byte, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	status, ok := f.status[actorID+"\x00"+key]
	return status, f.body[actorID+"\x00"+key], ok
}

const testActor = "0192f2a0-0000-7000-8000-000000000001"

// mutationRoute builds the route the ledger endpoints will be, reduced to the
// part these guards are about: a key is required, something is written, and
// the answer has to survive a retry.
func mutationRoute(d api.Deps, replayed func() bool, status int, body any) (http.Handler, *int) {
	ran := 0
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ran++
		api.WriteIdempotentResponseForTest(w, r, d, testActor, replayed(), status, body)
	})
	return api.RequireIdempotencyKeyForTest(d)(handler), &ran
}

func idempotentDeps(f *fakeIdempotency) api.Deps {
	return api.Deps{Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Ledger: &api.LedgerDeps{Idempotency: f}}
}

type createdBody struct {
	ID string `json:"id"`
}

func TestAMutationWithoutAnIdempotencyKeyIsRefused(t *testing.T) {
	t.Parallel()

	fake := newFakeIdempotency()
	d := idempotentDeps(fake)
	handler, ran := mutationRoute(d, func() bool { return false }, http.StatusCreated, createdBody{ID: "x"})

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/transactions", nil))

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Equal(t, "idempotency_key_required", errorCodeOf(t, rec))
	// The handler must not have run: a refused mutation that already wrote
	// something is not refused, whatever the status line says.
	require.Zero(t, *ran)
	require.Zero(t, fake.saves)
}

func TestMoreThanOneIdempotencyKeyIsRefused(t *testing.T) {
	t.Parallel()

	fake := newFakeIdempotency()
	d := idempotentDeps(fake)
	handler, ran := mutationRoute(d, func() bool { return false }, http.StatusCreated, createdBody{ID: "x"})

	r := httptest.NewRequest(http.MethodPost, "/api/v1/transactions", nil)
	r.Header.Add(api.IdempotencyKeyHeaderForTest, "one")
	r.Header.Add(api.IdempotencyKeyHeaderForTest, "two")

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, r)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Equal(t, "invalid_request", errorCodeOf(t, rec))
	require.Zero(t, *ran)
}

func TestUnusableIdempotencyKeysAreRefused(t *testing.T) {
	t.Parallel()

	for name, key := range map[string]string{
		"empty":            "",
		"over length":      strings.Repeat("k", api.MaxIdempotencyKeyLengthForTest+1),
		"tab":              "a\tb",
		"newline":          "a\nb",
		"nul":              "a\x00b",
		"delete":           "a\x7fb",
		"non ascii":        "kunci-é",
		"only whitespace":  "\v",
		"leading control":  "\x01abc",
		"trailing control": "abc\x1f",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			fake := newFakeIdempotency()
			d := idempotentDeps(fake)
			handler, ran := mutationRoute(d, func() bool { return false },
				http.StatusCreated, createdBody{ID: "x"})

			r := httptest.NewRequest(http.MethodPost, "/api/v1/transactions", nil)
			// Set rather than Add, so an empty value is still one header.
			r.Header[http.CanonicalHeaderKey(api.IdempotencyKeyHeaderForTest)] = []string{key}

			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, r)

			require.Equal(t, http.StatusBadRequest, rec.Code)
			require.Zero(t, *ran)
		})
	}
}

func TestAUsableKeyReachesTheHandlerUnaltered(t *testing.T) {
	t.Parallel()

	// Surrounding spaces are the case that matters. A key is opaque and is
	// compared byte-for-byte on the retry, so trimming it here would decide
	// that two different keys are one.
	const key = "  spaced key  "

	fake := newFakeIdempotency()
	d := idempotentDeps(fake)
	handler, ran := mutationRoute(d, func() bool { return false },
		http.StatusCreated, createdBody{ID: "x"})

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, requestWithKey(key))

	require.Equal(t, http.StatusCreated, rec.Code)
	require.Equal(t, 1, *ran)

	_, _, ok := fake.recorded(testActor, key)
	require.True(t, ok, "the key was altered on the way through")
}

func TestAFirstAttemptRecordsWhatItAnswered(t *testing.T) {
	t.Parallel()

	const key = "key-1"
	fake := newFakeIdempotency()
	d := idempotentDeps(fake)
	handler, _ := mutationRoute(d, func() bool { return false },
		http.StatusCreated, createdBody{ID: "abc"})

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, requestWithKey(key))

	require.Equal(t, http.StatusCreated, rec.Code)

	status, body, ok := fake.recorded(testActor, key)
	require.True(t, ok)
	require.Equal(t, http.StatusCreated, status)
	// The recorded bytes are the bytes that were sent, not something equal to
	// them after a round trip.
	require.JSONEq(t, `{"id":"abc"}`, string(body))
	require.Equal(t, string(body), strings.TrimSpace(rec.Body.String()))
}

func TestAReplayRepeatsTheOriginalStatusAndBytes(t *testing.T) {
	t.Parallel()

	const key = "key-2"
	fake := newFakeIdempotency()
	d := idempotentDeps(fake)

	replayed := false
	// The second attempt renders a different body at a different status. A
	// replay that reports either of them has not replayed — and a create
	// answered 201 coming back 200 is the exact failure migration 9 exists
	// for.
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if replayed {
			api.WriteIdempotentResponseForTest(w, r, d, testActor, true,
				http.StatusOK, createdBody{ID: "different"})
			return
		}
		api.WriteIdempotentResponseForTest(w, r, d, testActor, false,
			http.StatusCreated, createdBody{ID: "original"})
	})
	route := api.RequireIdempotencyKeyForTest(d)(handler)

	first := httptest.NewRecorder()
	route.ServeHTTP(first, requestWithKey(key))
	require.Equal(t, http.StatusCreated, first.Code)

	replayed = true
	second := httptest.NewRecorder()
	route.ServeHTTP(second, requestWithKey(key))

	require.Equal(t, http.StatusCreated, second.Code, "a replay answered with the fresh status")
	require.Equal(t, first.Body.String(), second.Body.String())
	require.Contains(t, second.Body.String(), "original")
	require.NotContains(t, second.Body.String(), "different")
}

func TestAReplayWithNoRecordedResponseAnswersAndRepairsTheRecord(t *testing.T) {
	t.Parallel()

	// The window this covers: the claim is taken inside the write's own
	// database transaction, so it exists as soon as the write commits, and
	// the response is recorded a moment later. A process that stops between
	// the two leaves exactly this state.
	const key = "key-3"
	fake := newFakeIdempotency()
	d := idempotentDeps(fake)
	handler, ran := mutationRoute(d, func() bool { return true },
		http.StatusCreated, createdBody{ID: "recovered"})

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, requestWithKey(key))

	require.Equal(t, http.StatusCreated, rec.Code)
	require.JSONEq(t, `{"id":"recovered"}`, rec.Body.String())
	require.Equal(t, 1, *ran)

	status, body, ok := fake.recorded(testActor, key)
	require.True(t, ok, "the missing record was not repaired")
	require.Equal(t, http.StatusCreated, status)
	require.JSONEq(t, `{"id":"recovered"}`, string(body))
}

func TestRecordingFailureDoesNotFailACommittedWrite(t *testing.T) {
	t.Parallel()

	fake := newFakeIdempotency()
	fake.failSave = errors.New("no")
	d := idempotentDeps(fake)
	handler, _ := mutationRoute(d, func() bool { return false },
		http.StatusCreated, createdBody{ID: "written"})

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, requestWithKey("key-4"))

	// The write has already committed by the time the response is recorded.
	// Answering 500 would tell the caller their mutation failed when it did
	// not, and the retry that follows is a replay of something they were told
	// never happened.
	require.Equal(t, http.StatusCreated, rec.Code)
	require.JSONEq(t, `{"id":"written"}`, rec.Body.String())
}

func TestAFailureToReadARecordedResponseIsNotAnsweredWithAGuess(t *testing.T) {
	t.Parallel()

	fake := newFakeIdempotency()
	fake.failRead = errors.New("no")
	d := idempotentDeps(fake)
	handler, _ := mutationRoute(d, func() bool { return true },
		http.StatusCreated, createdBody{ID: "guess"})

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, requestWithKey("key-5"))

	// A recorded answer may exist and be unreadable. Rendering a fresh one
	// would risk contradicting what the caller was already told.
	require.Equal(t, http.StatusInternalServerError, rec.Code)
	require.Equal(t, "internal", errorCodeOf(t, rec))
	require.NotContains(t, rec.Body.String(), "guess")
}

func TestAHandlerReachedWithoutTheMiddlewareReportsAnInternalError(t *testing.T) {
	t.Parallel()

	fake := newFakeIdempotency()
	d := idempotentDeps(fake)

	// Deliberately not wrapped: this is the programming error of mounting a
	// mutation without requireIdempotencyKey in front of it.
	bare := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		api.WriteIdempotentResponseForTest(w, r, d, testActor, false,
			http.StatusCreated, createdBody{ID: "x"})
	})

	rec := httptest.NewRecorder()
	require.NotPanics(t, func() {
		bare.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/transactions", nil))
	})
	require.Equal(t, http.StatusInternalServerError, rec.Code)
	require.Zero(t, fake.saves)
}

func TestAKeyReusedForADifferentRequestIsRefused(t *testing.T) {
	t.Parallel()

	fake := newFakeIdempotency()
	d := idempotentDeps(fake)

	rec := httptest.NewRecorder()
	api.WriteIdempotencyConflictForTest(rec, d)

	require.Equal(t, http.StatusConflict, rec.Code)
	require.Equal(t, "idempotency_key_reused", errorCodeOf(t, rec))
}

func requestWithKey(key string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/api/v1/transactions", nil)
	r.Header[http.CanonicalHeaderKey(api.IdempotencyKeyHeaderForTest)] = []string{key}
	return r
}

func errorCodeOf(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	raw, err := io.ReadAll(rec.Body)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(raw, &body), "response was not an error document: %s", raw)
	return body.Error.Code
}
