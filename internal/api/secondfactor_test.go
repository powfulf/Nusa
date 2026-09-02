// SPDX-License-Identifier: AGPL-3.0-only

package api_test

import (
	"encoding/base32"
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"

	"github.com/powfulf/Nusa/internal/api"
	"github.com/powfulf/Nusa/internal/auth"
)

// Setting up a second factor over HTTP.
//
// What these guards must cover, decided before any of them was written.
//
//	1. Every route that changes a second factor is refused without a current
//	   code — enumerated from the route table, not written out one endpoint at
//	   a time, so a route added later is covered by a guard nobody had to
//	   remember to write.
//	2. The table and the router agree in both directions. A route in one and
//	   not the other is what makes an enumerating guard quietly stop covering
//	   things.
//	3. The secret is shown once, at enrolment, and never again — checked
//	   across the status, the headers and the body.
//	4. Backup codes are shown when issued and never read back; the count
//	   endpoint returns a number and nothing else.
//	5. An unconfirmed enrolment is not a second factor: sign-in stays one step
//	   until confirmation succeeds.
//	6. Reissuing discards the previous set, spent codes included.
//	7. A one-time code cannot be used twice, because this path goes through
//	   the store's replay protection rather than around it.
//	8. A backup code is accepted where a one-time code would be, and is spent.
//	9. Enrolling over a confirmed factor is refused.
//
// Guard 3 and guard 4 read the whole response surface. A guard that promises
// "this does not leak X" and reads only the body is half a guard — a break
// that leaked through a header proved that once already (§13).

// wholeResponse renders everything a caller can see: the status, the headers
// and the body.
func wholeResponse(r response) string {
	var b strings.Builder
	b.WriteString(strings.Join([]string{"status", http.StatusText(r.code)}, ": "))
	for name, values := range r.header {
		b.WriteString("\n" + name + ": " + strings.Join(values, ","))
	}
	b.WriteString("\n" + r.body)
	return b.String()
}

// enrolled takes an account all the way to a confirmed second factor through
// the API, and returns the session and the secret.
func enrolled(t *testing.T) (*harness, string, []byte) {
	t.Helper()

	h := newHarness(t)
	h.registered()
	login := h.login(testEmail, testPassword)
	require.Equal(t, http.StatusOK, login.code, login.body)
	cookie := login.sessionCookie()

	begin := h.do(http.MethodPost, "/api/v1/auth/totp", nil, cookie)
	require.Equal(t, http.StatusCreated, begin.code, begin.body)

	var started struct {
		Secret string `json:"secret"`
		URI    string `json:"uri"`
	}
	require.NoError(t, json.Unmarshal([]byte(begin.body), &started))
	secret, err := base32.StdEncoding.WithPadding(base32.NoPadding).
		DecodeString(started.Secret)
	require.NoError(t, err)

	confirm := h.do(http.MethodPost, "/api/v1/auth/totp/confirm",
		map[string]string{"code": h.totpCode(secret)}, cookie)
	require.Equal(t, http.StatusCreated, confirm.code, confirm.body)

	// Confirming spends the counter the code proved, exactly as signing in
	// would, so the same code is not usable again. Every caller wants a fresh
	// window, and advancing here rather than in each of them keeps that fact
	// in one place.
	h.advance(time.Minute)

	return h, cookie, secret
}

// 1. The enumerating guard. It walks the table rather than naming endpoints,
// so a route added to the table is covered without a test being written for
// it — and a route added to neither is caught by guard 2.
func TestEveryRouteThatChangesASecondFactorDemandsOne(t *testing.T) {
	for _, route := range api.SecondFactorRoutesForTest {
		if !route.RequiresCode {
			continue
		}
		t.Run(route.Method+" "+route.Path, func(t *testing.T) {
			h, cookie, _ := enrolled(t)

			// A session alone. Without the requirement, a stolen session would
			// be enough to remove the control that exists precisely because a
			// password can be stolen.
			res := h.do(route.Method, route.Path, map[string]string{}, cookie)
			require.Equal(t, http.StatusUnauthorized, res.code, res.body)
			require.Equal(t, "second_factor_required", res.errorCode(t))

			// And a wrong one is no better than none.
			wrong := h.do(route.Method, route.Path, map[string]string{"code": "000000"}, cookie)
			require.Equal(t, http.StatusUnauthorized, wrong.code, wrong.body)
			require.Equal(t, "invalid_credentials", wrong.errorCode(t))
		})
	}
}

// 2. The table and the router, in both directions.
func TestTheSecondFactorRouteTableMatchesTheRouter(t *testing.T) {
	h := newHarness(t)

	mux, ok := h.router.(chi.Routes)
	require.True(t, ok, "the router must be walkable for this guard to mean anything")

	mounted := map[string]bool{}
	require.NoError(t, chi.Walk(mux,
		func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
			if strings.HasPrefix(route, "/api/v1/auth/totp") ||
				strings.HasPrefix(route, "/api/v1/auth/backup-codes") {
				mounted[method+" "+strings.TrimSuffix(route, "/")] = true
			}
			return nil
		}))

	tabled := map[string]bool{}
	for _, r := range api.SecondFactorRoutesForTest {
		tabled[r.Method+" "+r.Path] = true
	}

	require.NotEmpty(t, mounted, "the walk found no routes, so this compares nothing")
	require.Equal(t, sortedKeys(tabled), sortedKeys(mounted),
		"the route table and the router disagree; a route in one and not the other "+
			"means the enumerating guard is covering something else than what is served")
}

// 3. The secret, once.
func TestTheTOTPSecretIsShownOnceAndNeverAgain(t *testing.T) {
	h, cookie, secret := enrolled(t)
	encoded := auth.EncodeSecret(secret)

	// Nothing that can be asked for afterwards repeats it.
	for _, probe := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/auth/session"},
		{http.MethodGet, "/api/v1/auth/backup-codes"},
	} {
		res := h.do(probe.method, probe.path, nil, cookie)
		require.NotContains(t, wholeResponse(res), encoded,
			"%s %s repeated the secret", probe.method, probe.path)
	}

	// Nor does beginning again, which is refused outright while a confirmed
	// factor exists.
	again := h.do(http.MethodPost, "/api/v1/auth/totp", nil, cookie)
	require.Equal(t, http.StatusConflict, again.code, again.body)
	require.Equal(t, "second_factor_already_set", again.errorCode(t))
	require.NotContains(t, wholeResponse(again), encoded)
}

// 4. Backup codes are issued and never read back.
func TestBackupCodesAreIssuedOnceAndOnlyCounted(t *testing.T) {
	h, cookie, secret := enrolled(t)

	// Confirmation issued them; capture what was shown.
	reissue := h.do(http.MethodPost, "/api/v1/auth/backup-codes",
		map[string]string{"code": h.totpCode(secret)}, cookie)
	require.Equal(t, http.StatusCreated, reissue.code, reissue.body)

	var issued struct {
		Codes []string `json:"backup_codes"`
	}
	require.NoError(t, json.Unmarshal([]byte(reissue.body), &issued))
	require.Len(t, issued.Codes, auth.BackupCodeCount)

	count := h.do(http.MethodGet, "/api/v1/auth/backup-codes", nil, cookie)
	require.Equal(t, http.StatusOK, count.code, count.body)
	require.JSONEq(t, `{"unused":10}`, count.body)

	// The whole surface, not just the body: a list of backup codes readable
	// from a live session is a second password that never expires.
	whole := wholeResponse(count)
	for _, code := range issued.Codes {
		require.NotContains(t, whole, code, "the count endpoint leaked a code")
	}
}

// 5. An enrolment that was never confirmed is not a second factor.
func TestAnUnconfirmedEnrolmentDoesNotTurnOnTwoFactor(t *testing.T) {
	h := newHarness(t)
	h.registered()

	first := h.login(testEmail, testPassword)
	require.Equal(t, http.StatusOK, first.code)
	cookie := first.sessionCookie()

	begin := h.do(http.MethodPost, "/api/v1/auth/totp", nil, cookie)
	require.Equal(t, http.StatusCreated, begin.code, begin.body)

	// Signing in again must still be one step. If beginning an enrolment
	// switched 2FA on, somebody who closed the tab before scanning the QR code
	// would be locked out of their own instance.
	second := h.login(testEmail, testPassword)
	require.Equal(t, http.StatusOK, second.code, second.body)
	// A sign-in that needs a second factor is not an error: it answers 200 and
	// names the next step in the body. Reading errorCode here would find
	// nothing either way and prove nothing.
	require.Equal(t, "authenticated", second.sessionStatus(t),
		"an unconfirmed enrolment switched two-factor on")

	var started struct {
		Secret string `json:"secret"`
	}
	require.NoError(t, json.Unmarshal([]byte(begin.body), &started))
	secret, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(started.Secret)
	require.NoError(t, err)

	confirm := h.do(http.MethodPost, "/api/v1/auth/totp/confirm",
		map[string]string{"code": h.totpCode(secret)}, cookie)
	require.Equal(t, http.StatusCreated, confirm.code, confirm.body)

	// And now it is one.
	h.advance(time.Minute)
	third := h.login(testEmail, testPassword)
	require.Equal(t, http.StatusOK, third.code, third.body)
	require.Equal(t, "second_factor_required", third.sessionStatus(t))
}

// 5b. An unconfirmed enrolment demands no code, because there is none to give.
//
// This covers the branch in requireSecondFactorCode that lets an unconfirmed
// enrolment through. Without it the request reaches ConsumeTOTPCode, which
// answers ErrTOTPNotConfirmed — not a wrong code but an operational fault —
// and the caller gets a 500 for doing something perfectly reasonable. Nothing
// covered that until the branch was removed on purpose and the whole suite
// stayed green.
func TestAnUnconfirmedEnrolmentDemandsNoCode(t *testing.T) {
	h := newHarness(t)
	h.registered()
	cookie := h.login(testEmail, testPassword).sessionCookie()

	require.Equal(t, http.StatusCreated,
		h.do(http.MethodPost, "/api/v1/auth/totp", nil, cookie).code)

	// Somebody who began an enrolment and has not finished it still has no
	// second factor, so there is nothing to present and nothing to protect.
	res := h.do(http.MethodPost, "/api/v1/auth/backup-codes", map[string]string{}, cookie)
	require.Equal(t, http.StatusCreated, res.code, res.body)
}

// 6. Reissuing discards the old set.
func TestReissuingBackupCodesInvalidatesTheOldOnes(t *testing.T) {
	h, cookie, secret := enrolled(t)

	confirmBody := h.do(http.MethodPost, "/api/v1/auth/backup-codes",
		map[string]string{"code": h.totpCode(secret)}, cookie)
	require.Equal(t, http.StatusCreated, confirmBody.code, confirmBody.body)
	var first struct {
		Codes []string `json:"backup_codes"`
	}
	require.NoError(t, json.Unmarshal([]byte(confirmBody.body), &first))

	h.advance(time.Minute)
	again := h.do(http.MethodPost, "/api/v1/auth/backup-codes",
		map[string]string{"code": h.totpCode(secret)}, cookie)
	require.Equal(t, http.StatusCreated, again.code, again.body)
	var second struct {
		Codes []string `json:"backup_codes"`
	}
	require.NoError(t, json.Unmarshal([]byte(again.body), &second))
	require.NotEqual(t, first.Codes, second.Codes)

	// Both sets really were stored. Without this the test passes when nothing
	// was stored at all — an old code is refused either way, and "discarded"
	// and "never kept" look identical from the door.
	count := h.do(http.MethodGet, "/api/v1/auth/backup-codes", nil, cookie)
	require.JSONEq(t, `{"unused":10}`, count.body, "the reissued set was not stored")

	// The old list is gone entirely, unspent codes included: somebody
	// reissuing has usually decided it was compromised.
	h.advance(time.Minute)
	res := h.do(http.MethodDelete, "/api/v1/auth/totp",
		map[string]string{"code": first.Codes[0]}, cookie)
	require.Equal(t, http.StatusUnauthorized, res.code, res.body)
	require.Equal(t, "invalid_credentials", res.errorCode(t))

	// And a code from the new set is accepted, which is what makes the refusal
	// above about *which* set rather than about there being none.
	accepted := h.do(http.MethodDelete, "/api/v1/auth/totp",
		map[string]string{"code": second.Codes[0]}, cookie)
	require.Equal(t, http.StatusNoContent, accepted.code, accepted.body)
}

// 7. The store's replay protection is on this path, not beside it.
func TestAOneTimeCodeCannotBeUsedTwiceOnThisPath(t *testing.T) {
	h, cookie, secret := enrolled(t)

	code := h.totpCode(secret)
	first := h.do(http.MethodPost, "/api/v1/auth/backup-codes",
		map[string]string{"code": code}, cookie)
	require.Equal(t, http.StatusCreated, first.code, first.body)

	// The same code, inside the same window. A handler calling
	// Authenticator.Validate directly would accept this: the check would pass
	// and nothing would have spent the counter. Going through
	// ConsumeTOTPCode is what makes the second attempt fail.
	second := h.do(http.MethodPost, "/api/v1/auth/backup-codes",
		map[string]string{"code": code}, cookie)
	require.Equal(t, http.StatusUnauthorized, second.code, second.body)
	require.Equal(t, "invalid_credentials", second.errorCode(t))
}

// 8. A backup code stands in for the authenticator that was lost.
func TestABackupCodeIsAcceptedWhereAOneTimeCodeWouldBe(t *testing.T) {
	h, cookie, secret := enrolled(t)

	issued := h.do(http.MethodPost, "/api/v1/auth/backup-codes",
		map[string]string{"code": h.totpCode(secret)}, cookie)
	require.Equal(t, http.StatusCreated, issued.code)
	var codes struct {
		Codes []string `json:"backup_codes"`
	}
	require.NoError(t, json.Unmarshal([]byte(issued.body), &codes))

	// Somebody whose authenticator is gone is exactly who needs to disable it.
	res := h.do(http.MethodDelete, "/api/v1/auth/totp",
		map[string]string{"code": codes.Codes[0]}, cookie)
	require.Equal(t, http.StatusNoContent, res.code, res.body)

	// And it is spent: one backup code, one use.
	h.advance(time.Minute)
	begin := h.do(http.MethodPost, "/api/v1/auth/totp", nil, cookie)
	require.Equal(t, http.StatusCreated, begin.code, begin.body,
		"the factor was not actually removed")
}

// 9. Confirming with a wrong code leaves 2FA off.
func TestConfirmingWithAWrongCodeDoesNotEnable(t *testing.T) {
	h := newHarness(t)
	h.registered()
	cookie := h.login(testEmail, testPassword).sessionCookie()

	require.Equal(t, http.StatusCreated,
		h.do(http.MethodPost, "/api/v1/auth/totp", nil, cookie).code)

	res := h.do(http.MethodPost, "/api/v1/auth/totp/confirm",
		map[string]string{"code": "000000"}, cookie)
	require.Equal(t, http.StatusUnauthorized, res.code, res.body)
	require.Equal(t, "invalid_credentials", res.errorCode(t))

	h.advance(time.Minute)
	after := h.login(testEmail, testPassword)
	require.Equal(t, http.StatusOK, after.code, after.body)
	require.Equal(t, "authenticated", after.sessionStatus(t),
		"a refused confirmation switched the factor on anyway")
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
