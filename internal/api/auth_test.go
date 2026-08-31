// SPDX-License-Identifier: AGPL-3.0-only

package api_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/GaffaQ/Nusa/internal/api"
	"github.com/GaffaQ/Nusa/internal/auth"
)

// What these guards must cover. Written before they were, and each item was
// broken on purpose and watched failing afterwards, against a prediction
// written first (CLAUDE.md §11).
//
// Registration
//
//	 1. The first account registers and is not signed in by registering.
//	 2. A refused registration is byte-identical whether the instance is full
//	    or the address is taken — no leak either way.
//	 3. A short password and a malformed address are refused with a field.
//
// Sign-in
//
//	 4. Correct credentials set a cookie and report authenticated.
//	 5. A wrong password and an unknown address produce byte-identical
//	    responses.
//	 6. The unknown-address path really calls VerifyDecoy.
//	 7. The limiter is consulted before any credential work, and a blocked
//	    address is answered without it.
//	 8. A success clears the count.
//
// Second factor
//
//	 9. A confirmed enrolment makes sign-in two-phase.
//	10. A pending session reaches the second-factor route and sign-out, and
//	    nothing else.
//	11. Verifying rotates the token: the old cookie dies, the new one works.
//	12. A backup code is accepted where a one-time code would be.
//
// Sessions
//
//	13. Sign-out revokes; the token stops working even for a client that kept
//	    its cookie.
//	14. The session is resolved on every request — nothing is cached — so a
//	    revocation elsewhere takes effect on the next request.
//	15. A revocation during an in-flight request is effective for concurrent
//	    and subsequent requests.
//
// Audit
//
//	16. Actor-attributable events are recorded; failed sign-ins are not, which
//	    is what keeps the two failure paths identical.

const (
	testEmail    = "budi@example.org"
	testPassword = "kopi tubruk gula aren"
	otherPass    = "something else entirely"
)

type harness struct {
	t       *testing.T
	router  http.Handler
	creds   *fakeCredentials
	session *fakeSessions
	factors *fakeSecondFactors
	events  *fakeEvents
	hasher  *countingHasher
	limiter *auth.Limiter
	deps    api.Deps

	mu  sync.Mutex
	now time.Time
	ids int
}

func newHarness(t *testing.T) *harness {
	t.Helper()

	inner, err := auth.NewHasher(auth.Params{
		Memory: 64, Iterations: 1, Parallelism: 1, SaltLength: 8, TagLength: 16,
	})
	require.NoError(t, err)

	limiter, err := auth.NewLimiter(5, time.Minute)
	require.NoError(t, err)

	creds := newFakeCredentials()
	h := &harness{
		t:       t,
		creds:   creds,
		session: newFakeSessions(creds),
		factors: newFakeSecondFactors(),
		events:  &fakeEvents{},
		hasher:  &countingHasher{inner: inner},
		limiter: limiter,
		now:     time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC),
	}

	h.deps = api.Deps{
		Logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
		Database:      stubDatabase{version: 9},
		HealthTimeout: time.Second,
		Auth: &api.AuthDeps{
			Credentials:   h.creds,
			Sessions:      h.session,
			SecondFactors: h.factors,
			Events:        h.events,
			Hasher:        h.hasher,
			Authenticator: auth.DefaultAuthenticator(),
			Limiter:       limiter,
			SessionTTL:    24 * time.Hour,
			SecureCookies: false,
			Now:           h.clock,
			NewID:         h.nextID,
		},
	}
	h.router = api.NewRouter(h.deps)
	return h
}

// newHarnessWithLedger is newHarness with the ledger routes mounted.
//
// The auth side stays fake and the ledger side is real, which is deliberate:
// the round-trip tests are about what comes out of the database, and
// authentication is proved against its own store elsewhere. What the
// arrangement does show incidentally is that the ledger routes really are
// behind requireAuthenticated rather than merely intended to be.
func newHarnessWithLedger(t *testing.T, ledgerDeps *api.LedgerDeps) *harness {
	t.Helper()

	h := newHarness(t)
	h.deps.Ledger = ledgerDeps
	h.router = api.NewRouter(h.deps)
	return h
}

// apiTestID builds a canonical lowercase UUIDv7 from a label, so identities
// are stable across runs and readable in a failure message. Identities always
// come from outside the domain, and a test is just another outside.
func apiTestID(label string) string {
	sum := sha256.Sum256([]byte(label))
	var b [16]byte
	copy(b[:], sum[:16])
	b[6] = (b[6] & 0x0f) | 0x70 // version 7
	b[8] = (b[8] & 0x3f) | 0x80 // RFC 4122 variant

	const hexDigits = "0123456789abcdef"
	out := make([]byte, 0, 36)
	for i, c := range b {
		if i == 4 || i == 6 || i == 8 || i == 10 {
			out = append(out, '-')
		}
		out = append(out, hexDigits[c>>4], hexDigits[c&0x0f])
	}
	return string(out)
}

func (h *harness) clock() time.Time {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.now
}

func (h *harness) advance(d time.Duration) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.now = h.now.Add(d)
}

// nextID mints deterministic canonical UUIDv7 values so a failure message
// names something readable.
func (h *harness) nextID() (string, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.ids++
	return fmt.Sprintf("0195c0de-%04d-7000-8000-%012d", h.ids, h.ids), nil
}

type response struct {
	code    int
	body    string
	cookies []*http.Cookie
	header  http.Header
}

func (r response) errorCode(t *testing.T) string {
	t.Helper()
	var parsed struct {
		Error struct {
			Code    string            `json:"code"`
			Field   string            `json:"field"`
			Details map[string]string `json:"details"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal([]byte(r.body), &parsed), "body was %q", r.body)
	return parsed.Error.Code
}

// errorDetail reads one machine-readable specific out of a failure. It is what
// separates a validation_failed a client can act on from one it cannot.
func (r response) errorDetail(t *testing.T, key string) string {
	t.Helper()
	var parsed struct {
		Error struct {
			Details map[string]string `json:"details"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal([]byte(r.body), &parsed), "body was %q", r.body)
	return parsed.Error.Details[key]
}

// errorField reads which parameter or field a failure names. A refusal that
// does not say what was wrong leaves the caller guessing between everything
// they sent.
func (r response) errorField(t *testing.T) string {
	t.Helper()
	var parsed struct {
		Error struct {
			Field string `json:"field"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal([]byte(r.body), &parsed), "body was %q", r.body)
	return parsed.Error.Field
}

func (r response) sessionCookie() string {
	for _, c := range r.cookies {
		if strings.HasSuffix(c.Name, "nusa_session") && c.Value != "" {
			return c.Value
		}
	}
	return ""
}

func (h *harness) do(method, path string, body any, cookie string) response {
	h.t.Helper()

	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		require.NoError(h.t, err)
		reader = bytes.NewReader(encoded)
	}
	r := httptest.NewRequest(method, path, reader)
	r.RemoteAddr = "203.0.113.10:44321"
	if cookie != "" {
		r.AddCookie(&http.Cookie{Name: "nusa_session", Value: cookie})
	}

	w := httptest.NewRecorder()
	h.router.ServeHTTP(w, r)
	return response{code: w.Code, body: w.Body.String(), cookies: w.Result().Cookies(), header: w.Header()}
}

// doKeyed is do with an Idempotency-Key, which every mutation route requires.
func (h *harness) doKeyed(method, path, key string, body any, cookie string) response {
	h.t.Helper()

	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		require.NoError(h.t, err)
		reader = bytes.NewReader(encoded)
	}
	r := httptest.NewRequest(method, path, reader)
	r.RemoteAddr = "203.0.113.10:44321"
	r.Header.Set("Idempotency-Key", key)
	if cookie != "" {
		r.AddCookie(&http.Cookie{Name: "nusa_session", Value: cookie})
	}

	w := httptest.NewRecorder()
	h.router.ServeHTTP(w, r)
	return response{code: w.Code, body: w.Body.String(), cookies: w.Result().Cookies(), header: w.Header()}
}

func (h *harness) register(email, password string) response {
	return h.do(http.MethodPost, "/api/v1/auth/register",
		map[string]string{"email": email, "password": password}, "")
}

func (h *harness) login(email, password string) response {
	return h.do(http.MethodPost, "/api/v1/auth/login",
		map[string]string{"email": email, "password": password}, "")
}

// registered creates the instance's account and returns its id.
func (h *harness) registered() string {
	h.t.Helper()
	res := h.register(testEmail, testPassword)
	require.Equal(h.t, http.StatusCreated, res.code, "body %s", res.body)
	var body struct {
		UserID string `json:"user_id"`
	}
	require.NoError(h.t, json.Unmarshal([]byte(res.body), &body))
	return body.UserID
}

// 1, 3.
func TestRegistration(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	res := h.register(testEmail, testPassword)
	require.Equal(t, http.StatusCreated, res.code, res.body)
	require.Empty(t, res.sessionCookie(),
		"registering must not sign anybody in: the password was chosen, not presented")

	t.Run("refuses a short password by field", func(t *testing.T) {
		h := newHarness(t)
		res := h.register(testEmail, "short")
		require.Equal(t, http.StatusBadRequest, res.code)
		require.Equal(t, "invalid_request", res.errorCode(t))
		require.Contains(t, res.body, `"field":"password"`)
		require.Contains(t, res.body, "min_length")
	})

	t.Run("refuses a malformed address by field", func(t *testing.T) {
		h := newHarness(t)
		res := h.register("not-an-address", testPassword)
		require.Equal(t, http.StatusBadRequest, res.code)
		require.Contains(t, res.body, `"field":"email"`)
	})
}

// 2. The leak test for decision (d).
//
// After the first account exists, every registration is refused — and the two
// reasons it can be refused must be indistinguishable. "Registration is
// closed" tells an anonymous caller that somebody is using this instance;
// "that address is taken" tells them who. Both are the same shape of leak as
// account enumeration.
func TestARefusedRegistrationRevealsNothing(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.registered()

	// The instance is full, and the address is new: "closed".
	closed := h.register("someone.else@example.org", testPassword)
	// The instance is full, and the address is the one already taken.
	taken := h.register(testEmail, testPassword)

	require.Equal(t, http.StatusConflict, closed.code)
	require.Equal(t, closed.code, taken.code, "the status must not distinguish the two")
	require.Equal(t, closed.body, taken.body,
		"the bodies differ, so a caller can tell a taken address from a closed instance")
	require.Equal(t, "registration_unavailable", closed.errorCode(t))

	// And the response says nothing about either cause in passing.
	for _, forbidden := range []string{"taken", "closed", "already", testEmail} {
		require.NotContains(t, strings.ToLower(closed.body), strings.ToLower(forbidden),
			"the refusal names a cause")
	}
}

// 4, 5, 6.
func TestSignIn(t *testing.T) {
	t.Parallel()

	t.Run("correct credentials", func(t *testing.T) {
		h := newHarness(t)
		userID := h.registered()

		res := h.login(testEmail, testPassword)
		require.Equal(t, http.StatusOK, res.code, res.body)
		require.NotEmpty(t, res.sessionCookie())
		require.Contains(t, res.body, `"status":"authenticated"`)
		require.Contains(t, res.body, userID)

		// The cookie carries the attributes §10 fixes.
		var cookie *http.Cookie
		for _, c := range res.cookies {
			if c.Value == res.sessionCookie() {
				cookie = c
			}
		}
		require.NotNil(t, cookie)
		require.True(t, cookie.HttpOnly, "script must not be able to read the token")
		require.Equal(t, http.SameSiteLaxMode, cookie.SameSite)
		require.Equal(t, "/", cookie.Path)
		require.Empty(t, cookie.Domain, "a host-only cookie is not sent to subdomains")
	})

	t.Run("a wrong password and an unknown address answer identically", func(t *testing.T) {
		h := newHarness(t)
		h.registered()

		wrongPassword := h.login(testEmail, otherPass)
		unknownAddress := h.login("nobody@example.org", testPassword)

		require.Equal(t, http.StatusUnauthorized, wrongPassword.code)
		require.Equal(t, wrongPassword.code, unknownAddress.code,
			"the status distinguishes a registered address from an unregistered one")
		require.Equal(t, wrongPassword.body, unknownAddress.body,
			"the body distinguishes a registered address from an unregistered one")
		require.Equal(t, "invalid_credentials", wrongPassword.errorCode(t))
	})

	t.Run("the unknown-address path spends a verification", func(t *testing.T) {
		h := newHarness(t)
		h.registered()

		require.Zero(t, h.hasher.decoyCount())

		h.login("nobody@example.org", testPassword)
		require.Equal(t, 1, h.hasher.decoyCount(),
			"the no-such-account path returned without calling VerifyDecoy, so it answers "+
				"in microseconds while a real address costs a full Argon2id run")

		// A wrong password must *not* spend a decoy: it already spent a real
		// verification. Counting both would make the failure path cost twice
		// what the success path does, which is a difference in the other
		// direction.
		h.login(testEmail, otherPass)
		require.Equal(t, 1, h.hasher.decoyCount())
	})
}

// 7, 8.
func TestSignInIsRateLimited(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.registered()

	for i := range 5 {
		res := h.login(testEmail, otherPass)
		require.Equal(t, http.StatusUnauthorized, res.code, "attempt %d", i)
	}

	blocked := h.login(testEmail, otherPass)
	require.Equal(t, http.StatusTooManyRequests, blocked.code)
	require.Equal(t, "rate_limited", blocked.errorCode(t))
	require.NotEmpty(t, blocked.header.Get("Retry-After"))

	t.Run("a blocked address is refused before any credential work", func(t *testing.T) {
		// The decoy count is the probe. A blocked attempt against an unknown
		// address must not reach the lookup at all, so it must not spend one.
		before := h.hasher.decoyCount()
		res := h.do(http.MethodPost, "/api/v1/auth/login",
			map[string]string{"email": "nobody@example.org", "password": testPassword}, "")
		require.Equal(t, http.StatusTooManyRequests, res.code)
		require.Equal(t, before, h.hasher.decoyCount(),
			"a blocked attempt still performed credential work, so the limiter is not "+
				"protecting the server from the cost of an attack")
	})

	t.Run("the count clears on success", func(t *testing.T) {
		// A fresh harness, and deliberately *not* advancing past the window.
		// Waiting out the window would clear the count on its own, so the test
		// would pass whether or not a success cleared anything — established by
		// removing the Succeed call and watching the first draft stay green.
		h := newHarness(t)
		h.registered()

		for range 4 {
			require.Equal(t, http.StatusUnauthorized, h.login(testEmail, otherPass).code)
		}
		require.Equal(t, http.StatusOK, h.login(testEmail, testPassword).code,
			"the fifth attempt is still inside the allowance")

		// The allowance was 5 and 4 were spent. Without the clear, the second
		// of these would be refused as rate limited rather than as a wrong
		// password.
		for i := range 4 {
			require.Equal(t, http.StatusUnauthorized, h.login(testEmail, otherPass).code,
				"attempt %d was rate limited, so the successful sign-in did not clear the count", i)
		}
	})
}

// enrolTOTP puts a confirmed second factor on a user and returns the secret.
func (h *harness) enrolTOTP(userID string) []byte {
	h.t.Helper()
	secret, err := auth.NewSecret()
	require.NoError(h.t, err)
	require.NoError(h.t, h.factors.BeginTOTPEnrolment(context.Background(), userID, secret, h.clock()))
	h.factors.confirm(userID, h.clock())
	return secret
}

func (h *harness) totpCode(secret []byte) string {
	h.t.Helper()
	code, err := auth.DefaultAuthenticator().Code(secret, h.clock())
	require.NoError(h.t, err)
	return code
}

// 9, 10, 11.
func TestTwoPhaseSignIn(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	userID := h.registered()
	secret := h.enrolTOTP(userID)

	first := h.login(testEmail, testPassword)
	require.Equal(t, http.StatusOK, first.code, first.body)
	require.Contains(t, first.body, `"status":"second_factor_required"`)

	pending := first.sessionCookie()
	require.NotEmpty(t, pending, "the pending session is a credential and must be handed out")

	t.Run("a pending session reaches nothing else", func(t *testing.T) {
		res := h.do(http.MethodGet, "/api/v1/auth/session", nil, pending)
		require.Equal(t, http.StatusUnauthorized, res.code,
			"a cookie handed out at the password step reached an authenticated route")
		require.Equal(t, "second_factor_required", res.errorCode(t))
	})

	t.Run("a wrong code is refused", func(t *testing.T) {
		res := h.do(http.MethodPost, "/api/v1/auth/second-factor",
			map[string]string{"code": "000000"}, pending)
		require.Equal(t, http.StatusUnauthorized, res.code)
		require.Equal(t, "invalid_credentials", res.errorCode(t))
	})

	t.Run("the right code elevates and rotates the token", func(t *testing.T) {
		h.advance(time.Minute) // a window the wrong-code attempt did not spend
		res := h.do(http.MethodPost, "/api/v1/auth/second-factor",
			map[string]string{"code": h.totpCode(secret)}, pending)
		require.Equal(t, http.StatusOK, res.code, res.body)
		require.Contains(t, res.body, `"status":"authenticated"`)

		elevated := res.sessionCookie()
		require.NotEmpty(t, elevated)
		require.NotEqual(t, pending, elevated,
			"the token was not rotated, so whatever learned the pre-authentication "+
				"cookie now holds a fully authenticated one")

		// The old cookie is dead.
		old := h.do(http.MethodGet, "/api/v1/auth/session", nil, pending)
		require.Equal(t, http.StatusUnauthorized, old.code)
		require.Equal(t, "unauthenticated", old.errorCode(t))

		// The new one works.
		fresh := h.do(http.MethodGet, "/api/v1/auth/session", nil, elevated)
		require.Equal(t, http.StatusOK, fresh.code, fresh.body)
		require.Contains(t, fresh.body, userID)
	})
}

// 12.
func TestABackupCodeCompletesTheSecondFactor(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	userID := h.registered()
	h.enrolTOTP(userID)

	codes, err := auth.NewBackupCodes()
	require.NoError(t, err)
	require.NoError(t, h.factors.ReplaceBackupCodes(context.Background(), userID, codes, h.clock()))

	pending := h.login(testEmail, testPassword).sessionCookie()
	require.NotEmpty(t, pending)

	res := h.do(http.MethodPost, "/api/v1/auth/second-factor",
		map[string]string{"code": codes[2]}, pending)
	require.Equal(t, http.StatusOK, res.code, res.body)

	t.Run("and is single use", func(t *testing.T) {
		next := h.login(testEmail, testPassword).sessionCookie()
		res := h.do(http.MethodPost, "/api/v1/auth/second-factor",
			map[string]string{"code": codes[2]}, next)
		require.Equal(t, http.StatusUnauthorized, res.code)
	})
}

// 13.
func TestSignOutRevokesRatherThanForgetting(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.registered()

	token := h.login(testEmail, testPassword).sessionCookie()
	require.Equal(t, http.StatusOK, h.do(http.MethodGet, "/api/v1/auth/session", nil, token).code)

	out := h.do(http.MethodPost, "/api/v1/auth/logout", nil, token)
	require.Equal(t, http.StatusNoContent, out.code)

	// The cookie is cleared for the browser that asked.
	var cleared bool
	for _, c := range out.cookies {
		if strings.HasSuffix(c.Name, "nusa_session") && c.Value == "" && c.MaxAge < 0 {
			cleared = true
		}
	}
	require.True(t, cleared, "the cookie was not cleared")

	// And — the part that matters — the token itself no longer works, for a
	// client that kept it. Clearing a cookie deletes one copy of a credential
	// from one browser; it does nothing about a token already read out of it,
	// which is the case somebody signs out *because of*.
	after := h.do(http.MethodGet, "/api/v1/auth/session", nil, token)
	require.Equal(t, http.StatusUnauthorized, after.code,
		"the session was forgotten by the browser but not revoked on the server")
	require.Equal(t, "unauthenticated", after.errorCode(t))
}

// 10, continued: a pending session may sign out.
func TestAPendingSessionMaySignOut(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	userID := h.registered()
	h.enrolTOTP(userID)

	pending := h.login(testEmail, testPassword).sessionCookie()
	require.NotEmpty(t, pending)

	// A pending session holds a live cookie. Being unable to withdraw a live
	// credential is a worse position than being able to, so sign-out is the
	// one other route it may reach.
	out := h.do(http.MethodPost, "/api/v1/auth/logout", nil, pending)
	require.Equal(t, http.StatusNoContent, out.code)

	// The error code, not just the status, is what proves the revocation
	// happened. A wrong code answers 401 too, so asserting the status alone
	// would pass whether or not sign-out revoked anything — established by
	// removing the revocation and watching the first draft stay green.
	after := h.do(http.MethodPost, "/api/v1/auth/second-factor",
		map[string]string{"code": "000000"}, pending)
	require.Equal(t, http.StatusUnauthorized, after.code)
	require.Equal(t, "unauthenticated", after.errorCode(t),
		"the session was still resolvable, so sign-out did not revoke it — the 401 came "+
			"from the wrong code rather than from the session being gone")
}

// 14. The property only this layer can establish.
//
// The store proves a revoked session stops resolving. What that cannot show is
// whether the middleware ever asks: a cache holding sessions for even a few
// seconds would leave a revoked token working for that long and the store's
// test would still pass.
func TestTheSessionIsResolvedOnEveryRequest(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.registered()
	token := h.login(testEmail, testPassword).sessionCookie()

	before := h.session.Lookups()
	for range 5 {
		require.Equal(t, http.StatusOK, h.do(http.MethodGet, "/api/v1/auth/session", nil, token).code)
	}
	require.Equal(t, before+5, h.session.Lookups(),
		"five requests produced fewer than five lookups, so something is caching the session")

	// Revoked out of band, as another device signing this one out would.
	sessions, err := h.session.UserSessions(context.Background(), h.creds.users[testEmail].UserID)
	require.NoError(t, err)
	require.Len(t, sessions, 1)
	require.NoError(t, h.session.RevokeSession(context.Background(), sessions[0].ID, h.clock()))

	require.Equal(t, http.StatusUnauthorized,
		h.do(http.MethodGet, "/api/v1/auth/session", nil, token).code,
		"the very next request still succeeded after revocation")
}

// 15. A revocation while a request is in flight.
//
// The overlap is arranged rather than hoped for: the handler blocks until the
// test releases it, so the revocation provably commits while the first request
// is still inside the handler.
//
// What this proves is that revocation is effective the instant it commits, for
// concurrent and subsequent requests. What it does not prove — and what is
// stated here rather than implied — is that the request already inside the
// handler is interrupted. It is not. Nothing short of cancelling its context
// mid-flight would do that, and for Nusa's endpoints, which are all short
// reads and writes, the window is milliseconds. The day there is a long-lived
// endpoint is the day a cancellation watcher earns the goroutine it costs.
func TestARevocationDuringAnInFlightRequest(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.registered()
	token := h.login(testEmail, testPassword).sessionCookie()

	sessions, err := h.session.UserSessions(context.Background(), h.creds.users[testEmail].UserID)
	require.NoError(t, err)
	sessionID := sessions[0].ID

	started := make(chan struct{})
	release := make(chan struct{})
	guarded := api.RequireAuthenticatedForTest(h.deps)(http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) {
			close(started)
			<-release
			w.WriteHeader(http.StatusOK)
		}))

	inFlight := httptest.NewRecorder()
	firstDone := make(chan struct{})
	go func() {
		defer close(firstDone)
		r := httptest.NewRequest(http.MethodGet, "/whatever", nil)
		r.RemoteAddr = "203.0.113.10:44321"
		r.AddCookie(&http.Cookie{Name: "nusa_session", Value: token})
		guarded.ServeHTTP(inFlight, r)
	}()

	<-started // the first request is past the middleware and inside the handler
	require.NoError(t, h.session.RevokeSession(context.Background(), sessionID, h.clock()))

	// A second request, issued while the first is still running, is refused.
	concurrent := h.do(http.MethodGet, "/api/v1/auth/session", nil, token)
	require.Equal(t, http.StatusUnauthorized, concurrent.code,
		"a request starting after the revocation was still admitted")

	close(release)
	<-firstDone
	require.Equal(t, http.StatusOK, inFlight.Code,
		"the in-flight request is not interrupted, which is the documented limit")
}

// 16.
func TestWhatReachesTheAuditLog(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.registered()

	require.Equal(t, []string{"account_registered"}, h.events.actions())

	// Failures are deliberately absent. Writing a row on the wrong-password
	// path and not on the no-such-account path — which cannot write one,
	// because it has no actor to attribute — would reintroduce through timing
	// exactly the difference VerifyDecoy exists to remove. Failed attempts go
	// to the structured log instead, identically on both paths.
	h.login(testEmail, otherPass)
	h.login("nobody@example.org", testPassword)
	require.Equal(t, []string{"account_registered"}, h.events.actions(),
		"a failed sign-in wrote an audit row, which makes the two failure paths "+
			"cost different amounts of work")

	token := h.login(testEmail, testPassword).sessionCookie()
	require.Equal(t, []string{"account_registered", "sign_in"}, h.events.actions())

	h.do(http.MethodPost, "/api/v1/auth/logout", nil, token)
	require.Equal(t, []string{"account_registered", "sign_in", "sign_out"}, h.events.actions())

	for _, e := range h.events.recorded {
		require.NotEmpty(t, e.ActorID, "%s has no actor", e.Action)
		require.NotEmpty(t, e.EntityKind)
		require.NotEmpty(t, e.ID)
	}
}
