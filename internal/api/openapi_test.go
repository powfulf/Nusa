// SPDX-License-Identifier: AGPL-3.0-only

package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"

	"github.com/powfulf/Nusa/internal/api"
	"github.com/powfulf/Nusa/internal/ledger"
	"github.com/powfulf/Nusa/internal/store"
)

// The OpenAPI document, checked against the implementation.
//
// A hand-written spec can drift from the code on more axes than whether a
// route exists, so each axis it can drift on is either guarded here or
// labelled as unguarded. What is NOT checked, stated rather than left to look
// covered:
//
//   - That a documented status is *reachable*. Guard 3 goes the other way:
//     every status the suite actually observed must be documented. Nothing
//     proves a documented status can occur, because proving it would mean
//     provoking every failure mode of every handler, and a 500 in particular
//     cannot be provoked without breaking something. A status documented and
//     never returned is a promise nobody collects on, which is the cheaper of
//     the two mistakes.
//   - Request bodies against their schemas. The handlers refuse unknown fields
//     and the DTOs are Go structs, so a request shape that the spec gets wrong
//     is caught the first time somebody follows the spec — not by anything
//     here.
//   - Descriptions. Nothing checks that prose is true. The guards below assert
//     the *load-bearing* phrases only: PROVISIONAL on disposing, and the
//     always-null claim on the two unpaginated listings.
//
// What these guards must cover, decided before any of them was written.
//
//	1. Every route the router serves appears in the document.
//	2. Every path and method in the document exists in the router. This is the
//	   direction usually left out, and its failure costs more: a spec promising
//	   an endpoint that is not there is followed until a client hits it.
//	3. Every status the suite observed is documented for that route.
//	4. The document's error-code list matches the codes the package defines,
//	   in both directions — and the Go inventory matches the constant block, so
//	   the inventory cannot itself go stale.
//	5. Money, Date and Rate are documented as the domain types actually
//	   marshal them, not as somebody remembered.
//	6. next_cursor is documented as always null for accounts and commodities,
//	   and disposing is marked PROVISIONAL.
//	7. The document is valid JSON and declares OpenAPI 3.1.

// routingStub satisfies the ledger interfaces so the router mounts every
// route. It panics if called: these guards are about routing and shape, and a
// stub that answered would invite a guard that quietly tests the stub.
type routingStub struct{}

func (routingStub) IdempotentResponse(context.Context, string, string) (int, []byte, bool, error) {
	panic("routing only")
}

func (routingStub) SaveIdempotentResponse(context.Context, string, string, int, []byte) error {
	panic("routing only")
}
func (routingStub) LoadCommodities(context.Context) ([]ledger.Commodity, error) {
	panic("routing only")
}
func (routingStub) LoadAccounts(context.Context) ([]ledger.Account, error) { panic("routing only") }

func (routingStub) LoadAccount(context.Context, ledger.AccountID) (ledger.Account, error) {
	panic("routing only")
}

func (routingStub) LoadTransaction(context.Context, ledger.TransactionID) (ledger.Transaction, error) {
	panic("routing only")
}

func (routingStub) TransactionsPage(context.Context, store.TransactionQuery) (store.TransactionPage, error) {
	panic("routing only")
}
func (routingStub) SaveAccount(context.Context, ledger.Account) error { panic("routing only") }

func (routingStub) UpdateAccount(context.Context, ledger.AccountID, string, bool) (ledger.Account, error) {
	panic("routing only")
}

func (routingStub) SaveTransaction(context.Context, store.Write, ledger.Transaction, ...ledger.Lot) (store.Result, error) {
	panic("routing only")
}

func (routingStub) SaveDisposal(context.Context, store.Write, ledger.Transaction, ...ledger.PostingID) (store.DisposalResult, error) {
	panic("routing only")
}

// fullyMountedRouter is the router with every route present, which is what
// both directions of the route guard have to compare against.
func fullyMountedRouter(t *testing.T) chi.Routes {
	t.Helper()
	h := newHarnessWithLedger(t, &api.LedgerDeps{
		Idempotency: routingStub{}, Journal: routingStub{}, Writer: routingStub{},
	})
	routes, ok := h.router.(chi.Routes)
	require.True(t, ok, "the router must be walkable for these guards to mean anything")
	return routes
}

type openAPIDoc struct {
	OpenAPI string                                `json:"openapi"`
	Paths   map[string]map[string]json.RawMessage `json:"paths"`

	Components struct {
		Schemas map[string]json.RawMessage `json:"schemas"`
	} `json:"components"`
}

type operation struct {
	Responses map[string]json.RawMessage `json:"responses"`
}

func spec(t *testing.T) openAPIDoc {
	t.Helper()
	var doc openAPIDoc
	require.NoError(t, json.Unmarshal(api.OpenAPIDocumentForTest, &doc),
		"the embedded document is not valid JSON")
	return doc
}

// mountedRoutes walks the router and returns "METHOD /path" for each.
func mountedRoutes(t *testing.T) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	require.NoError(t, chi.Walk(fullyMountedRouter(t),
		func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
			out[method+" "+strings.TrimSuffix(route, "/")] = true
			return nil
		}))
	return out
}

// documentedRoutes returns the same shape from the specification.
func documentedRoutes(t *testing.T) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	for path, methods := range spec(t).Paths {
		for method := range methods {
			// "parameters" is a sibling of the operations, not one of them.
			if method == "parameters" {
				continue
			}
			out[strings.ToUpper(method)+" "+path] = true
		}
	}
	return out
}

// 1 and 2.
func TestTheDocumentAndTheRouterAgreeInBothDirections(t *testing.T) {
	mounted := mountedRoutes(t)
	documented := documentedRoutes(t)

	// Both sides asserted non-empty before comparing. A diff of two empty sets
	// is green, and that is how a check of this shape reported success once
	// before (§11).
	require.NotEmpty(t, mounted, "the walk found no routes")
	require.NotEmpty(t, documented, "the document declares no paths")

	var undocumented, unmounted []string
	for route := range mounted {
		if !documented[route] {
			undocumented = append(undocumented, route)
		}
	}
	for route := range documented {
		if !mounted[route] {
			unmounted = append(unmounted, route)
		}
	}
	sort.Strings(undocumented)
	sort.Strings(unmounted)

	require.Empty(t, undocumented,
		"served but not documented: a client reading the spec cannot find these")
	require.Empty(t, unmounted,
		"documented but not served: a client will follow these until it hits a 404 at runtime")
}

// 3. Every status the suite actually saw is documented for that route.
//
// The comparison runs in TestMain, after everything else, because the
// observations accumulate across the whole package. It is one direction only,
// and the file header says why the other is not attempted.
func checkObservedStatusesAreDocumented() error {
	var doc openAPIDoc
	if err := json.Unmarshal(api.OpenAPIDocumentForTest, &doc); err != nil {
		return err
	}

	observed := takeObservedStatuses()
	if len(observed) == 0 {
		return errNoObservations
	}

	var missing []string
	compared := 0
	for key := range observed {
		parts := strings.SplitN(key, " ", 3)
		method, path, status := parts[0], parts[1], parts[2]

		operations, ok := doc.Paths[path]
		if !ok {
			// Guard 1 reports this far more usefully than a status mismatch
			// would, so it is left to that one.
			continue
		}
		raw, ok := operations[strings.ToLower(method)]
		if !ok {
			continue
		}
		compared++
		var op operation
		if err := json.Unmarshal(raw, &op); err != nil {
			return err
		}
		if _, ok := op.Responses[status]; !ok {
			missing = append(missing, key)
		}
	}
	// Counted, not assumed. Every observation whose path does not appear in
	// the document is skipped, so a normalisation that stopped matching would
	// skip all of them and this check would pass by comparing nothing — which
	// is the failure it is least able to notice about itself (§11).
	if compared < minimumStatusComparisons {
		return fmt.Errorf(
			"only %d of %d observed responses were matched against the document; "+
				"expected at least %d, so this check is reading almost nothing",
			compared, len(observed), minimumStatusComparisons)
	}

	sort.Strings(missing)
	if len(missing) > 0 {
		return fmt.Errorf(
			"these responses were returned by the suite and are not documented: %s",
			strings.Join(missing, ", "))
	}
	return nil
}

// minimumStatusComparisons is a floor on how much guard 3 must actually read.
//
// The number is deliberately well below what the suite produces today — it is
// there to catch a check that has stopped matching anything, not to pin the
// suite's exact shape, which would make every new test a failure.
const minimumStatusComparisons = 20

// 4. The error codes, in both directions, and the inventory against the source.
func TestTheDocumentedErrorCodesAreTheCodesThePackageDefines(t *testing.T) {
	var doc struct {
		Components struct {
			Schemas struct {
				Error struct {
					Properties struct {
						Error struct {
							Properties struct {
								Code struct {
									Enum []string `json:"enum"`
								} `json:"code"`
							} `json:"properties"`
						} `json:"error"`
					} `json:"properties"`
				} `json:"Error"`
			} `json:"schemas"`
		} `json:"components"`
	}
	require.NoError(t, json.Unmarshal(api.OpenAPIDocumentForTest, &doc))

	documented := doc.Components.Schemas.Error.Properties.Error.Properties.Code.Enum
	defined := make([]string, 0, len(api.AllErrorCodesForTest))
	for _, code := range api.AllErrorCodesForTest {
		defined = append(defined, string(code))
	}

	require.NotEmpty(t, documented, "the document lists no error codes")
	sort.Strings(documented)
	sort.Strings(defined)
	require.Equal(t, defined, documented,
		"a code in the spec that the server never sends is a catalogue entry a client "+
			"translates for nothing; a code the server sends that the spec omits is a "+
			"string a client has no rendering for")

	// And the inventory against the constant block, so the inventory cannot
	// itself go stale — otherwise it becomes the third place to forget.
	source, err := os.ReadFile("errors.go")
	require.NoError(t, err)
	declared := regexp.MustCompile(`Code[A-Za-z]+ ErrorCode = "([a-z_]+)"`).
		FindAllStringSubmatch(string(source), -1)
	require.NotEmpty(t, declared, "the constant block was not found; this guard is reading nothing")

	fromSource := make([]string, 0, len(declared))
	for _, m := range declared {
		fromSource = append(fromSource, m[1])
	}
	sort.Strings(fromSource)
	require.Equal(t, fromSource, defined,
		"allErrorCodes and the constant block disagree")
}

// 5. The money shapes, against what the domain types actually produce.
func TestTheDocumentedShapesMatchWhatTheDomainMarshals(t *testing.T) {
	doc := spec(t)

	// A value of each, marshalled through the domain's own MarshalJSON — which
	// is where §4.5 is enforced, and therefore the only honest reference.
	amount, err := ledger.ParseMoney("IDR", "1500000")
	require.NoError(t, err)
	date, err := ledger.NewDate(2026, 3, 7)
	require.NoError(t, err)
	rate, err := ledger.NewRate("USD", "IDR", big.NewRat(48001, 3))
	require.NoError(t, err)

	for name, value := range map[string]any{"Money": amount, "Date": date, "Rate": rate} {
		t.Run(name, func(t *testing.T) {
			encoded, err := json.Marshal(value)
			require.NoError(t, err)

			schema, ok := doc.Components.Schemas[name]
			require.True(t, ok, "the document has no %s schema", name)

			var declared struct {
				Type       any                        `json:"type"`
				Properties map[string]json.RawMessage `json:"properties"`
				Pattern    string                     `json:"pattern"`
			}
			require.NoError(t, json.Unmarshal(schema, &declared))

			var actual any
			require.NoError(t, json.Unmarshal(encoded, &actual))

			switch produced := actual.(type) {
			case string:
				// Date. The pattern in the spec has to match what came out.
				require.Regexp(t, declared.Pattern, produced,
					"%s marshals as %q, which the documented pattern rejects", name, produced)
			case map[string]any:
				keys := make([]string, 0, len(produced))
				for k, v := range produced {
					keys = append(keys, k)
					// Every field of a money value crosses as a string. A
					// number here would be the §4.5 violation the whole shape
					// exists to prevent.
					_, isString := v.(string)
					require.True(t, isString,
						"%s.%s marshalled as %T; the wire shape requires a string", name, k, v)

					// And the document must SAY string. Checking only what the
					// type marshals leaves the spec free to declare a number,
					// which is the one error a client would act on — it would
					// parse the amount as a float and lose precision above
					// 2^53, which is the whole reason §4.5 exists. The first
					// draft of this guard missed exactly that: a break that
					// documented the amount as a number left it green.
					property, ok := declared.Properties[k]
					require.True(t, ok, "%s.%s is not in the document", name, k)
					var field struct {
						Type any `json:"type"`
					}
					require.NoError(t, json.Unmarshal(property, &field))
					require.Equal(t, "string", field.Type,
						"%s.%s marshals as a string and the document declares %v", name, k, field.Type)
				}
				documented := make([]string, 0, len(declared.Properties))
				for k := range declared.Properties {
					documented = append(documented, k)
				}
				sort.Strings(keys)
				sort.Strings(documented)
				require.Equal(t, keys, documented,
					"%s marshals with different fields than the document declares", name)
			default:
				t.Fatalf("%s marshalled as %T, which this guard does not know how to check", name, actual)
			}
		})
	}
}

// 6. The two load-bearing claims in the prose.
func TestTheDocumentStatesWhatWasAgreed(t *testing.T) {
	text := string(api.OpenAPIDocumentForTest)

	// The unpaginated listings say so, so that the shared envelope is not a
	// promise of a second page that never comes.
	doc := spec(t)
	for _, name := range []string{"CommodityList", "AccountList"} {
		schema, ok := doc.Components.Schemas[name]
		require.True(t, ok, "no %s schema", name)

		var declared struct {
			Properties struct {
				NextCursor struct {
					Type        any    `json:"type"`
					Description string `json:"description"`
				} `json:"next_cursor"`
			} `json:"properties"`
		}
		require.NoError(t, json.Unmarshal(schema, &declared))
		require.Equal(t, "null", declared.Properties.NextCursor.Type,
			"%s must document next_cursor as null, not merely nullable", name)
		require.Contains(t, declared.Properties.NextCursor.Description, "ALWAYS null",
			"%s does not say the cursor is always null", name)
	}

	// disposing is marked provisional in the spec as well as in the code,
	// because a client reads the spec and never the code.
	require.Contains(t, text, "PROVISIONAL",
		"the disposing field must be marked provisional where a client will see it")

	var newTransaction struct {
		Properties struct {
			Disposing struct {
				Description string `json:"description"`
			} `json:"disposing"`
		} `json:"properties"`
	}
	require.NoError(t, json.Unmarshal(doc.Components.Schemas["NewTransaction"], &newTransaction))
	require.Contains(t, newTransaction.Properties.Disposing.Description, "PROVISIONAL")
}

// 7. Valid, versioned, and actually served.
func TestTheDocumentIsValidAndServed(t *testing.T) {
	require.Equal(t, "3.1.0", spec(t).OpenAPI)

	h := newHarness(t)
	res := h.do(http.MethodGet, "/openapi.json", nil, "")
	require.Equal(t, http.StatusOK, res.code)
	// Unauthenticated: a client needs the shape of the API before it has a
	// session, and a specification describes the shape rather than anything in
	// it.
	require.NotEmpty(t, res.body)

	var parsed map[string]any
	require.NoError(t, json.Unmarshal([]byte(res.body), &parsed))
	require.Equal(t, "3.1.0", parsed["openapi"])
}

// 8. Every $ref resolves.
//
// Structural validity against the full OpenAPI meta-schema is NOT checked
// here, and that is stated rather than implied: doing it properly needs a
// validator, and the nearest one is a Node package fetched at CI time — an
// unpinned network dependency for a document three guards already compare
// against the implementation. What is checked is the failure a hand-written
// document actually suffers: a reference to a component that was renamed or
// never written. A dangling $ref renders as a broken page in every viewer and
// breaks every generator, and nothing else here would notice it.
func TestEveryReferenceInTheDocumentResolves(t *testing.T) {
	var doc map[string]any
	require.NoError(t, json.Unmarshal(api.OpenAPIDocumentForTest, &doc))

	var refs []string
	var walk func(any)
	walk = func(node any) {
		switch value := node.(type) {
		case map[string]any:
			for k, v := range value {
				if k == "$ref" {
					if s, ok := v.(string); ok {
						refs = append(refs, s)
					}
					continue
				}
				walk(v)
			}
		case []any:
			for _, item := range value {
				walk(item)
			}
		}
	}
	walk(doc)

	require.NotEmpty(t, refs, "the document contains no references, so this guard read nothing")

	for _, ref := range refs {
		require.True(t, strings.HasPrefix(ref, "#/"),
			"%s is not a local reference; this document has no external ones", ref)

		node := any(doc)
		for _, segment := range strings.Split(strings.TrimPrefix(ref, "#/"), "/") {
			object, ok := node.(map[string]any)
			require.True(t, ok, "%s: %s is not an object", ref, segment)
			node, ok = object[segment]
			require.True(t, ok, "%s does not resolve: no %s", ref, segment)
		}
	}
}

// normalisePath turns a request path into the route pattern the document uses,
// so an observation can be looked up.
var uuidSegment = regexp.MustCompile(
	`/[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}`)

func normalisePath(path string) string {
	if i := strings.IndexByte(path, '?'); i >= 0 {
		path = path[:i]
	}
	return uuidSegment.ReplaceAllString(path, "/{id}")
}

func recordStatus(method, path string, status int) {
	observedMu.Lock()
	defer observedMu.Unlock()
	observedStatuses[method+" "+normalisePath(path)+" "+strconv.Itoa(status)] = true
}

func takeObservedStatuses() map[string]bool {
	observedMu.Lock()
	defer observedMu.Unlock()
	return observedStatuses
}
