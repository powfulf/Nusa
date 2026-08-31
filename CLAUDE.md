# CLAUDE.md — Nusa

> Place this file at the repository root. Claude Code reads it automatically in every session.
> This file is **normative**. If a request conflicts with it, raise the conflict before writing code.

---

## 1. What Nusa is

A self-hostable, open-source personal finance application. One correct double-entry ledger underneath; budgeting, investments, and planning built on top of it.

**Core thesis:** every competitor picks two of these four. Nusa does all four.

1. Correct double-entry accounting
2. Envelope budgeting that ordinary people can actually use
3. First-class multi-currency and multi-asset support
4. Usable in any country without bank APIs

**Everything country-specific lives in a Country Pack plugin. Never in core.**

## 2. Non-goals

- Nusa never asks for bank or broker credentials. Not optionally. Not "at the user's own risk." Never.
- Nusa does not execute trades, payments, or transfers. It is a record-keeping and planning tool.
- Nusa does not give financial advice. It computes; the user decides. No buy/sell recommendations, no product rankings.
- Nusa is not an accounting package for businesses. Personal and household only.

---

## 3. Stack

| Layer | Choice |
|---|---|
| Backend | Go 1.22+ |
| Database | PostgreSQL 16+ (only — no SQLite path) |
| HTTP | `net/http` + `chi` router |
| Migrations | `golang-migrate`, SQL files, forward-only |
| DB access | `sqlc` (generated, type-safe). No ORM. |
| Frontend | TypeScript, React 18, Vite |
| State | TanStack Query (server state) + Zustand (UI state). No Redux. |
| Styling | Tailwind CSS with a custom token layer (see §8) |
| Charts | Recharts |
| Plugin runtime | YAML (declarative) + Starlark via `go.starlark.net` (escape hatch) |
| Testing | Go: stdlib + `testify` + `rapid` (property-based). Frontend: Vitest + Playwright |
| Deploy | Docker Compose, single `docker compose up` |
| Dev tooling | Pinned in `tools/go.mod`, built into `bin/` by the Makefile |

**Go version.** The floor is 1.22, chosen deliberately for contributor reach. Raising it is a deliberate decision, not a consequence of a dependency upgrade. Tooling never sets the floor: `sqlc` and `golangci-lint` are pinned in `tools/go.mod`, so a linter needing a newer toolchain can never raise the version required to build the server.

`go mod tidy` raises the `go` directive on its own when any dependency — including a *test* dependency of a dependency, which nothing we ship ever executes — asks for a newer one. Always run it as `go mod tidy -go=1.22`, and pin the offending module back rather than accepting the bump. CI verifies the directive against the version it installs and runs `GOTOOLCHAIN=local` everywhere except the steps that build `tools/`; without that asymmetry Go would simply fetch the newer toolchain and every job would pass.

**Module path:** `github.com/GaffaQ/Nusa`. It must match the repository path **exactly, including the capital N**. Go treats a module path as case-sensitive while GitHub does not, so `github.com/gaffaq/nusa` resolves in a browser and then fails to match what the module proxy has recorded. Never normalise it to lowercase.

**The module path and the product name are separate things.** The product name lives in `internal/brand/brand.go` as constants and is not final; never hardcode "Nusa" in UI strings, because all user-facing text goes through i18n. Renaming the product does not move the module, and moving the repository does not rename the product.

**Environment variables use the `NUSA_` prefix.** This is a deliberate exception to the brand-isolation rule: env var names cannot be resolved at runtime. A future rename must update the prefix here and in `.env.example`.

---

## 4. Money — the rules that must never be broken

Money bugs destroy trust permanently. These rules are absolute.

**4.1 Never use float for money.** Not `float64`, not JS `number`, not `numeric` with decimal places for storage. If you see a float touching a monetary value, that is a bug regardless of context.

**4.2 The Money type.**

```go
type Money struct {
    Amount    *big.Int // in the commodity's smallest unit
    Commodity string   // "IDR", "USD", "BBCA.JK", "BTC", "XAU_GRAM"
}
```

Scale lives on the Commodity, not on Money. IDR = 2, USD = 2, BTC = 8, ETH = 18, IDX equities = 0 (whole shares), mutual fund units = 4.

**4.3 Arithmetic across commodities is a compile-time-impossible / runtime-error case.** `Add(IDR, USD)` returns an error. Never silently convert.

**4.4 Postgres storage:** `amount numeric(40,0) NOT NULL` (minor units as integer) + `commodity_code text NOT NULL`. Two columns, always together. Never one "amount in IDR" column.

**4.5 Frontend:** money crosses the wire as `{ "amount": "1500000", "commodity": "IDR" }` — amount is a **string**, because JS numbers lose precision above 2^53. Parse with BigInt. There is a `Money` TS class; use it, never raw arithmetic.

**4.6 Rounding:** banker's rounding (half-to-even) everywhere, applied once, at the last step. Never round intermediate values. Every rounding site must have a comment explaining why rounding happens there.

**4.7 The exact intermediate type.** Anything that cannot stay a whole number of smallest units — multiplication by a fraction, division, an applied exchange rate — returns `ledger.Rat` (`*big.Rat` + commodity), never `Money`. `Rat.Round` is the only conversion back and the only implementation of half-to-even in the codebase. This is what makes 4.6 enforceable rather than aspirational: rounding halfway through a calculation requires writing `Round` in the middle, where a reviewer sees it. A second rounding helper anywhere is a bug.

## 5. Ledger invariants

These are enforced in code and verified by property-based tests. A change that breaks one of them is not mergeable.

1. **A transaction's postings sum to zero, per commodity.** Multi-commodity transactions (e.g. buying USD with IDR) balance within each commodity via a conversion posting pair.
2. **An account balance equals the sum of its postings.** Always derivable; cached balances are an optimization that must be reconstructible from scratch.
3. **Postings are immutable.** Corrections create reversing entries, never mutate history. A "delete" is a tombstone.
4. **FX rates are stored on the posting**, at the moment of the transaction. Historical reports must never change when today's rate changes.
5. **Envelopes/budgets do not create postings.** Budget allocation is a virtual layer over the ledger. This is what lets us have both correct double-entry and usable envelope budgeting.
6. **Every write is idempotent.** Client generates a UUIDv7 for the entity plus an `Idempotency-Key` header. Replaying a request must never duplicate.
7. **Everything that can be pointed at has its own identity.** Accounts, transactions, postings and lots each carry a distinct ID type, validated as a canonical lowercase UUIDv7 and supplied from outside — the domain never generates one and never reads a clock. Posting identities are unique across the whole journal, not merely within their transaction. Nothing is ever referenced by its position in a slice: reordering is routine and silent, so a positional reference breaks without an error and leaves only a wrong number behind.

## 6. Architecture

```
cmd/nusa/              entrypoint
                       subcommands: serve (default), migrate up|down|version
db/                    migrations and sqlc queries, embedded into the binary
internal/
  ledger/              THE CORE. Money, Commodity, Account, Transaction, Posting, Lot.
                       Zero dependencies on http, sql, or config. Pure domain logic.
  budget/              envelopes, goals, rollover policy
  schedule/            recurring transactions, RRULE-like engine
  rules/               trigger -> action automation
  invest/              holdings, lots, cost basis, price providers
  scenario/            branchable what-if projections (D3)
  household/           multi-user books, visibility scopes (D17)
  countrypack/         plugin loader, YAML schema, Starlark sandbox
  api/                 HTTP handlers, DTOs, OpenAPI. Thin — no business logic here.
  store/               sqlc-generated queries + repository interfaces
  auth/                sessions, TOTP, password hashing
  i18n/                message catalogs, locale resolution
  config/              typed environment configuration, validated at startup.
                       Sits at the edge with cmd. Nothing in the domain imports it.
tools/                 separate module pinning sqlc and golangci-lint
web/                   React app
plugins/
  country-pack-id/     Indonesia
docs/                  VitePress documentation site
```

**Dependency direction is strictly inward.** `api` depends on `ledger`; `ledger` depends on nothing. If you find yourself importing `database/sql` inside `internal/ledger`, stop — the design is wrong.

**The ledger's purity is enforced, not merely documented.** `.golangci.yml` carries a depguard rule denying `database/sql`, `net/http`, `internal/config` and `internal/store` inside `internal/ledger`. A violating import fails CI rather than review.

**Migrations ship inside the binary.** `db/migrations` is embedded with `go:embed` and applied by `nusa migrate up`. There is no migration image, no mounted volume, and no way to deploy a container whose migration files came from a different build. `serve` never migrates implicitly: rolling out a binary and changing the schema stay separate, deliberate acts.

**One origin.** In production the Go server also serves the built frontend from `NUSA_WEB_DIR`, so there is no second service and no CORS configuration anywhere. When that directory is absent the server runs API-only — the normal state while Vite serves the app in development.

`internal/ledger` and the Country Pack specification are licensed MIT. Everything else is AGPL-3.0. Keep the `LICENSE` headers correct.

---

## 7. Language and vocabulary — read this before writing any UI string

Nusa's users are not accountants. Most have never heard of double-entry bookkeeping and never should.

**The technical model stays rigorous internally. The vocabulary is translated at the boundary.**

| Internal / technical | What the user sees (English) | Indonesian |
|---|---|---|
| Posting, journal entry, double-entry | *never shown at all* | — |
| Reconciliation | "Match with your real balance" | "Cocokkan dengan saldo asli" |
| Envelope / budget category | "Envelope" | "Amplop" |
| Piggy bank / sinking fund | "Savings jar" | "Celengan" |
| Net worth | "What you're worth" + one-line explainer | "Kekayaan bersih" |
| Cost basis | "What you paid" | "Harga beli awal" |
| Unrealized gain/loss | "On-paper gain — not money until you sell" | "Untung di atas kertas" |
| Unrealized FX gain | "Change from exchange rates, not from spending" | "Berubah karena kurs, bukan karena belanja" |
| Amortization schedule | "Payment plan" | "Rencana cicilan" |
| XIRR | "Yearly return, adjusted for when you invested" | "Imbal hasil tahunan" |

**Rules for UI copy:**
- No jargon without an inline explainer. Any potentially unfamiliar term gets a `<Explain term="...">` component: a small `?` that opens a three-sentence card **using the user's own numbers**.
- No sentence longer than 20 words in primary UI.
- Never blame the user. "You went over" not "You overspent again". Use coral, not alarm red.
- Numbers always carry a unit or currency. Never a bare "1.500.000".
- Every empty state teaches something. An empty transaction list explains what a transaction is and offers one action.

## 8. Design system

### 8.1 Visual authority — `DESIGN.md`

**`DESIGN.md` at the repository root is the single authority for how Nusa
looks.** Every colour, typeface, type step, spacing step, radius, shadow,
motion curve, icon, brand asset and component specification originates there
and nowhere else.

No visual value appears in this file. If you are looking for a hex code, a
pixel size or a font name, it is in `DESIGN.md`.

The chain runs one way and never the other:

```
DESIGN.md  ->  web/src/styles/tokens.css  ->  tailwind.config.js  ->  components
 (decides)         (transcribes)               (binds names)         (consume)
```

`web/src/styles/tokens.css` is the only file permitted to hold a raw colour or
length, and `web/src/styles/fonts.css` the only one permitted to name a
typeface. Neither decides anything; both transcribe `DESIGN.md`. A visual
change starts in `DESIGN.md` and is carried down. Changing a token without
changing `DESIGN.md` first is the one thing this arrangement exists to prevent.

Design decisions are never recorded in code comments, in token files, or in a
component library. One source, always.

### 8.2 Functional floor — binding on any design system

These are not style preferences and they do not belong to `DESIGN.md`. They
bind whatever design system Nusa uses, including a future replacement. Where
`DESIGN.md` and this floor disagree, **this floor wins and `DESIGN.md` is
corrected** — never the reverse, and never by quietly relaxing a check.

**Contrast.** Thresholds differ by role. A single uniform number produces both
false failures and false passes, so each pairing is checked as what it is:

| Role | Threshold |
| --- | --- |
| Normal text (under 18.66px bold / 24px) | 4.5:1 |
| Large text (18.66px bold and up, or 24px and up) | 3:1 |
| Icons and marks that carry meaning | 3:1 |
| A boundary that is a control's sole indicator | 3:1 |
| Focus indicators | 3:1, unconditional |
| Decorative borders and dividers | none |

Contrast is verified against every surface a value may appear on, not against
one representative background.

**Controls.** Every interactive control carries at least one indicator besides
its border — a fill that differs from its surroundings, a permanently visible
label, or an icon. A control identified only by a faint border fails 1.4.11,
and darkening every border to compensate is not the only remedy.

**Focus.** The focus ring holds 3:1 on every surface it can land on, including
dark fills, where a ring in the primary colour would sit at 1:1 and vanish.
Focus is the sole marker of keyboard position; nothing else rescues it.

**Numbers.** All numerals use `font-variant-numeric: tabular-nums`, align to
the inline end, and carry a consistent decimal count within a column. A
negative amount always shows an explicit minus sign; colour only accompanies
it. Amounts are never truncated or ellipsised — a truncated balance is a wrong
balance.

**Meaning.** Colour is never the sole carrier of meaning. Every colour-coded
state also carries an icon, a sign, or a word.

**Surfaces.** Text never sits on a translucent surface whose backdrop can
change: its contrast then depends on what happens to be behind it and cannot be
verified once. No gradients inside table cells.

**Internationalisation.** Logical CSS properties throughout
(`margin-inline-start`, never `margin-left`), so RTL is a stylesheet change
rather than a rewrite. Layouts never lock a width to the length of the English
string.

**Motion.** All motion honours `prefers-reduced-motion`. Nothing conveys
information by movement alone. A "reduce visual effects" control flattens
elevation and stops motion without removing any information.

**Enforcement.** `npm run lint:tokens` and `npm run test:contrast` must pass
before merge; both run in CI. The first rejects a raw colour, font or length
written outside the token files and any third-party asset host. The second
recomputes every pairing above from the shipped tokens. Neither may be
satisfied by editing the check.

## 9. Internationalization

- Default UI language: **English**. First translation: **Indonesian**. Both must exist from the first milestone that renders text.
- Zero hardcoded user-facing strings. Ever. `t('transaction.add')`, never `"Add transaction"`.
- Locale governs number format, date format, first day of week, and currency display independently of language.
- Layout must survive RTL. Use logical CSS properties (`margin-inline-start`, not `margin-left`) from day one — retrofitting RTL is brutal.
- Never concatenate translated fragments. Use full sentences with interpolation.
- Plural rules via ICU MessageFormat. Indonesian has no plural forms; Arabic has six. The library handles it — do not hand-roll.

## 10. Privacy and security

- No telemetry. If it is ever added, it is opt-in, documented, and inspectable.
- Passwords: Argon2id. Sessions: HttpOnly + Secure + SameSite=Lax cookies. TOTP for 2FA.
- **Client IP.** No middleware trusts `X-Forwarded-For`, `X-Real-IP` or `True-Client-IP`. Any feature that needs a client address — rate limiting first — must take an explicit list of trusted proxies from configuration. Blanket header trust lets a client forge its own address and defeat precisely the protection it is meant to enable.
- Every Country Pack data provider runs **server-side and cached per instance**, never per user. We do not flood third-party servers, and we do not leak per-user request patterns.
- Starlark plugins get no network, no filesystem, no clock beyond an injected deterministic one, and an execution step limit.
- Audit log for every mutation: who, when, what, and whether it originated from a human, a rule, or the AI layer.
- AI-proposed changes enter a `proposed` state and require explicit human approval. The AI layer never writes to the ledger directly.

## 11. Testing

- `internal/ledger` requires **property-based tests** covering the invariants in §5. This is not optional.
- §4.1's ban on floats binds test code too, generators included. A fixture that reaches a money value through a float is not verifying the rule, it is demonstrating the one way the rule gets broken. `grep -rE "float64|float32" internal/ledger/` must return nothing — comments included, so that the check stays a check rather than a judgement call.
- Domain tests live in `package ledger_test`. Every invariant in §5 is a promise made to callers, so it is proved through the same door a caller uses.
- Golden-file tests for every importer and every Country Pack.
- Every bug fix starts with a failing test that reproduces it.
- Coverage target: 85% in `internal/ledger`, 60% elsewhere. Coverage is a floor, not a goal.
- **A guard is not installed until you have watched it fail.** Decide what it must cover *before* writing it, then break each item on that list in turn and confirm the failure. "The check passes" is not evidence the check works — a check that reads nothing also passes.
- **Breaking the implementation is half of it. Check that what failed is what you expected to fail.** A guard can fire for a reason other than the one written on it, and neither a green run nor a red one shows the difference — the break produces a failure, the failure is taken as proof, and the claim in the comment is never tested at all. So name the test you expect to go red *before* running the break, and when a different one goes red instead, the comment is what is wrong. A guard whose stated claim is false is worse than a missing guard, because the next reader stops looking.
- **A guard must not borrow an external source's authority for something that source never said.** Published test vectors prove exactly what they cover and nothing adjacent to it; a check labelled as RFC-backed when the RFC is silent on the case is a false claim wearing a citation. Self-consistency checks are legitimate and often the only thing available — differential tests, round trips, invariants against a second implementation — but they are labelled as what they are, in the test, so nobody later mistakes them for proof from outside.
- **A comparison check must prove both sides are non-empty before it compares them.** A diff of two empty sets is green, a `grep` over a file that never arrived matches nothing, and a suite whose cases are all rejected early reports success. Every one of those looks exactly like a pass. Assert the size of what you are about to compare — line counts, row counts, case counts — and fail if it is zero, so the check cannot succeed by reading nothing. This has now happened twice: a property suite that got twelve times faster because its generator's output was being discarded, and a schema comparison that produced an empty diff because the SQL never reached the container.
- **A test that arranges the expected outcome by itself is testing nothing.** Ask what would happen if the function under test were deleted outright, not merely changed — if the assertion would still hold, the setup is producing the result and the subject is a passenger. The instance here: a check that a successful sign-in clears the rate-limit count first advanced the clock past the window, so the count had expired on its own and the test passed whether or not anything cleared it. Waiting out a timeout, seeding the answer, and asserting a default all fail this way, and all of them look like ordinary arrangement. The same shape reaches the assertion itself, through a disjunction one branch of which is always true: `require.True(t, errors.Is(err, ErrInvalidWrite) || ledger.ValidateID("not-a-uuid") != nil)` cannot fail, whatever the code does. Both are tests whose green does not depend on the subject — one arrives through the arrangement, the other through the assertion — and neither is found by running the suite, because both are already passing. They are found by reading the line and asking what would have to be true for it to go red.
- **Verification tooling is subject to the discipline it enforces.** A harness that applies a break, measures, and restores must restore *derived* artefacts as deliberately as it restores their sources: putting a `.sql` file back does not put the generated `.go` back, and the next measurement then runs against the previous break's code. The tool is not exempt from "watch it fail" merely because it is the thing doing the watching.
- **A fake more obedient than the real thing makes every guard above it pass while the property they rest on is unmet.** This is not the usual failure of a fake being wrong; the fake is *better behaved* than what it stands for, so the layer above it is proved against a world that does not exist. Thirteen guards over the HTTP idempotency layer all worked correctly and all ran against an in-memory store that returns exactly what it was handed — which is precisely what `jsonb` does not do: it reordered keys, dropped whitespace and discarded a duplicate key, so the "byte-for-byte replay" those guards sat on was false at the only layer that decides it.

  So: **wherever a fake stands in for an external system, write down which properties of that system the code above it relies on, and test those properties against the real thing.** Not against the fake, which will agree with any claim made about it. "It returns what it was given" is an assumption until it has been proved on real storage, and the input that proves it has to be chosen to break the promise — an awkward document, not a realistic one, because a well-behaved value passes under either implementation and distinguishes nothing.

  What found it was not breaking a guard. Every break behaved as predicted. It was refusing to take the phrase "byte-for-byte" in our own comment as given, and going to look at what the column does. **A claim we write about our own code is a candidate for testing, not a premise** — and the lower the layer that actually keeps the promise, the further the comment asserting it tends to sit from anything that checks it.
- **An index that makes a query fast can hide a clause that carries the rule.** A clause the query plan happens to satisfy cannot be falsified by any test running in an environment that always has that index: pull the clause out and everything stays green, and that is not evidence it was unnecessary. The instance here is the `ORDER BY txn_date, id` behind keyset pagination. Removing the identity left the whole suite passing, because the query is served by an Index Only Scan on `transactions_txn_date_idx` — which *is* `(txn_date, id)` — so the rows arrive in identity order whether or not the ORDER BY asks for it. On a bare table where the planner chose a sort instead, the two orders genuinely differed. The evidence came from `EXPLAIN`, not from a red test.

  Distinguish this from the `FOR UPDATE` in the backup-code path, which looked the same and was not: there the clause genuinely did nothing, removal changed no guarantee, and it was removed. Here the clause does something, and only the plan is standing in for it today.

  So: **a removal that produces no red test has not answered whether the clause carries weight. Find out what is satisfying it now.** If the answer is "the query plan, incidentally", the clause stays and is labelled — not deleted, and not treated as guarded either.

  This is the third of a kind, and they are one category rather than three incidents: the `len(trusted) == 0` branch in `api.ClientIP`, this ORDER BY tie-break, and the length prefix in the cursor filter digest. All three are correct, none is defended by any test, and each would survive deletion in silence. **The label they carry says four things**: that the line is correct, what satisfies it today, that no test will catch its removal, and what would make it matter — so the next reader neither deletes it believing it does nothing nor trusts a guard that is not there.

- **"Flaky" is a symptom, never a diagnosis.** A failure that will not reproduce is a fact needing an explanation, not noise to be waved away as the environment. Chase it until the cause is named, or record it openly as unexplained — and never let a green re-run stand as the explanation. The one time this was tested here, the "flaky test" was the harness lying: a contaminated run had failed a test that had nothing to do with the break, and only a written prediction that the results contradicted exposed it.
- **An assertion inside a property test is only as good as the generator feeding it.** Before trusting one, ask whether the generated data can even contain the thing being asserted about. Prove it by breaking the code that assertion covers and watching *that* test fail, not a neighbour.
- **A property test only tests what its generator varies. A field it never fills is compared perfectly and proves nothing.** This is a different failure from a generator whose cases are rejected early, and it is quieter: there, the suite announces itself by getting faster. Here nothing changes at all. Every case writes, reads and compares; two of the comparisons are between zero and zero, and the run is green, the timing is normal, and the check count is unchanged.

  The instance: `TestPropertyEverythingWrittenComesBackExactly` promised that everything written comes back exactly, and its generator had never set `OccurredAt` or `Timezone`. An audit of the rest found two more — `Account.Closed` was written by nothing but `false`, and two of the five account kinds were never stored at all, so dropping `liability` from the mapping that reads them back left the whole suite green.

  So: **check the claim against a list of fields, not against a green run.** And because a list held in somebody's head does not survive them, **every round-trip property test carries an explicit inventory of what its generator varies, in its own file, field by field** — including the fields deliberately not varied and why. A field added to a domain type will not add itself to a generator, and nothing will go red when it does not; the inventory is the only thing standing between that and a suite quietly promising more than it tests.

- **Isolate the field a guard is about.** If the case under test differs from the control in three ways, the guard is a test of none of them.
- **A concurrency test that does not force the interleaving is a test of the scheduler's mood.** Arrange the collision — hold the contended rows from the test itself — rather than starting goroutines and hoping.
- **A large change in how long a suite takes is a signal, and it must be chased in either direction.** A suite that suddenly got *faster* while still passing is the more suspicious of the two, precisely because nobody investigates good news: a slowdown gets a ticket, a speed-up gets a shrug. Absent a change that explains it, a suite that got much faster has usually stopped testing something, and it announces this by going green sooner.
- **A property test whose generator can produce input that is rejected early must report what fraction of cases actually reached the path under test.** Rejected input ends a case before the assertions run, so the check count says nothing about how many times the subject was exercised. Green with no such number is not evidence of coverage — it is evidence that something ran. Either count and log it, or arrange for rejected input to be repaired and the case continued.

## 12. Working conventions

- Conventional Commits. Semantic versioning. `main` is always releasable.
- **Commits carry no AI attribution.** No `Co-Authored-By` trailer naming an assistant, no "generated with" footer, and no mention of an assistant anywhere in the message body. The same applies to pull request descriptions. A commit describes the change and why it was made; who or what typed it is not part of the record. This overrides any tooling default that adds such a trailer.
- Every user-facing change updates `docs/`.
- No new dependency without a note in the PR explaining why the stdlib is insufficient.
- Errors: wrap with `fmt.Errorf("...: %w", err)`. Never `panic` outside `main`.
- Comments explain **why**, not what. If the what is unclear, rename things instead.
- When a task is ambiguous or conflicts with this file, **ask before writing code**.

---

## 13. Implementation notes

A running record, appended once per milestone. Sections above state the rules; this one states what was actually built, what went wrong, and what was consciously left for later. Rules that emerged from a milestone are promoted into the relevant section above — this log records the reasoning behind them, and the mistakes that produced them.

### M0 — Bootstrap

Skeleton only: no financial logic, no domain model, no endpoints beyond a health check.

#### Decisions and why

**PostgreSQL is reached through `pgx/v5` directly, not `database/sql`.** sqlc generates against the pgx interface, so there is no `sql.DB` layer and no driver registration anywhere. This keeps the generated code honest about Postgres types — `pgtype.Timestamptz` rather than a lossy `time.Time` round trip — and removes a whole abstraction that only pays for itself when you support more than one database. We do not.

**Configuration is resolved once, into a typed struct, and reports every problem at the same time.** An operator with three missing variables learns about all three from one failed start, not one restart at a time. The process exits before serving rather than panicking mid-request. See `internal/config`.

**The server verifies its database connection at startup and refuses to run without it.** A process that cannot reach its database is not healthy, and discovering that during a user's first write is far worse than discovering it at boot.

**The health endpoint reports status, never diagnosis.** It is unauthenticated, so it answers whether the database responded and at which schema version, and nothing else. Connection errors routinely carry internal hostnames and usernames; those go to the log. A failed check omits `schema_version` entirely rather than sending zero, so a consumer cannot read "unknown" as "version 0".

**`schema_meta` holds exactly one row, enforced by the schema itself** — a boolean primary key with a check constraint that rejects `false`, so a second insert violates the key. golang-migrate already tracks its own version; this table exists so the application can read a schema version over an ordinary connection without depending on the migration tool's private bookkeeping.

**Generated query code lives in the `store` package alongside the hand-written repository code**, rather than in a separate generated package. One package, one concern. sqlc owns `db.go`, `models.go`, `querier.go` and `*.sql.go`; everything else in that directory is hand-written and safe from regeneration.

**Frontend choices made once, so later milestones do not retrofit them:**

- **Tailwind v3, not v4.** §8's token layer is expressed through `theme.extend`, which is the v3 configuration model. Choosing v4 here would mean rewriting the token layer in M3.
- **TanStack Query from the first fetch.** It is the designated server-state tool in §3; using `useState` for the first request only to replace it in M4 is churn.
- **ICU MessageFormat wired from the first string,** even though M0 renders only a handful. §9 requires ICU plural rules, and retrofitting the message format across populated catalogs is far more work than starting with it.
- **Vitest with plain DOM assertions,** no `jest-dom` matcher package. Fewer dependencies, and the assertions read the same.
- **No ESLint.** `tsc --noEmit` under a strict configuration is the frontend gate. Adding a second linter with its own configuration and plugin set was not justified by anything M0 contains. Revisit if type checking proves insufficient.

**The Go server serves the built frontend, so there is one origin and no CORS anywhere** (recorded as a rule in §6). In development Vite serves the app and proxies to the API, which produces the same single-origin behaviour without a build step.

**Postgres is published to `127.0.0.1` only.** The port is configurable through `.env` because a developer machine frequently already has something on 5432 — this happened during M0 verification.

#### What went wrong, and the rule it produced

**`.gitignore` was UTF-16LE encoded, so git ignored nothing at all.** Git parses that file as bytes; a UTF-16 file is not readable to it and every rule silently does nothing. The first `git add -A` staged `.env` — which contains a database password — along with `node_modules` and local working notes. Nothing was committed, and the file was rewritten as UTF-8.
> **Rule.** Repository control files (`.gitignore`, `.dockerignore`, `.env`) must be ASCII or UTF-8, no BOM. Windows editors default to UTF-16 for some operations. After changing ignore rules, prove they work with `git check-ignore -v <path>` rather than assuming, and read the staged file list before the first commit of any new repository.

**A Makefile prerequisite named `node_modules` never matched the real directory `web/node_modules`.** Make treated the target as permanently missing and ran `npm ci` before every test, lint and build — and `npm ci` deletes the directory before reinstalling. Fixed by naming the target with its real path.
> **Rule.** A non-phony make target must be the actual filesystem path it stands for. Verify recipes with `make -n` before trusting them.

**Pinning development tools in the application's `go.mod` raised the module's minimum Go version to 1.26.** A linter dictated the version needed to build the server. Tools moved to `tools/go.mod`; see §3.
> **Rule.** Nothing that is not shipped may influence the application's dependency graph or its Go floor.

**`chi`'s `RealIP` middleware was wired in before anyone checked what it does.** It trusts `X-Forwarded-For`, `X-Real-IP` and `True-Client-IP` unconditionally, letting a client forge its own address. It is also deprecated with published advisories. Removed; the rule is now in §10.

**The product name was hardcoded in `web/index.html` as `<title>Nusa</title>`,** which defeats the brand isolation §3 requires. The title is now substituted at build time from the English message catalog and updated per language at runtime, so the name still has exactly one source.

**A verification result was reported more precisely than it was measured.** `make test` and `make lint` were reported as passing when `make` was not installed on the machine — the commands behind those targets had been run individually. Running the real target later exposed the `node_modules` bug that the individual commands could never have caught.
> **Rule.** Report the command that was actually run. If a check was approximated, say so, and treat "the underlying commands pass" as a different claim from "the target passes".

#### Deliberately deferred

| Deferred | Lands in |
| --- | --- |
| The entire domain model: money, commodities, accounts, transactions, postings, lots | M1 |
| Postgres schema beyond `schema_meta`, repositories, idempotency keys, audit log, OpenAPI | M2 |
| Authentication, sessions, TOTP, and rate limiting with an explicit trusted-proxy list | M2 |
| Design system primitives, Storybook, ambient layer, PWA | M3 |
| The "reduce visual effects" toggle. The `data-effects="reduced"` hook and its token overrides exist; the settings UI that flips the attribute does not | M3 |
| Playwright end-to-end tests. Vitest covers units today | M3–M4 |
| Automated WCAG AA contrast checks in CI. Tokens are defined but nothing verifies contrast yet | M3 |
| Country Pack runtime and Indonesian pack contents | M8 |
| VitePress documentation site. `docs/` exists with a placeholder README | M12 |
| Multi-architecture image builds, semantic-release, demo instance, backup tooling | M12 |
| Full RTL support. Logical CSS properties are used from day one so the retrofit stays small | Phase 1.5 |
| Light mode. Token names are theme-agnostic and the palette is scoped to `[data-theme="dark"]` | Planned, unscheduled |

### M1 — The domain core

`internal/ledger`, complete and dependency-free: money, commodities, exact intermediates, exchange rates, civil dates, accounts, transactions, postings, lots, and a journal to query them. No SQL, no HTTP, no configuration, nothing that reads a clock. 89.5% statement coverage against the 85% floor.

#### Decisions and why

**Money holds a commodity *code*; scale lives on a `Commodity` description resolved through a `Registry`.** §4.2 puts scale on the commodity, and this is the shape that enforces it: two values of the same commodity cannot disagree about where the decimal point sits, because neither of them carries the answer. The registry ships currencies and the two cryptocurrencies whose scales are properties of their chains. Equities, funds and metals come from Country Packs — an exchange's share scale is a country-specific fact.

**`Rate` holds codes rather than commodity descriptions, and conversion lives on `Registry`.** A rate gets written to a row and read back years later (§5.4), so it must serialise without embedding a scale — a stored scale is a second, staler copy of something the commodity already owns. But a rate is quoted in the units people say ("1 USD = 16.000 IDR") while amounts are counted in smallest units, so applying one needs both scales. `Registry.Convert` is where those two facts meet.

**`Rat.Round` takes no arguments.** A `Rat` is already denominated in smallest units, so rounding it needs no scale — the scale was applied when the amount was created. Passing a commodity in would have been ceremony that implied a decision was being made there.

**Civil dates are `Date{year, month, day}`, not a truncated `time.Time`.** Nothing can accidentally read a zone off them. `NewDate` refuses 30 February rather than normalising it to 2 March the way `time.Date` does: a date is a claim about what happened, and quietly relocating it is how a transaction lands in the wrong month. `DateOf(instant, location)` is the single sanctioned bridge from a timestamp, and it demands a location so the caller has to say whose day they mean.

**`PostingID` is a fourth distinct identity type**, alongside the three the planning session named. Approved during M1 review; the reasoning is worth keeping because it generalises.

A posting needs a stable name because things point at it: a lot records which posting opened it, an audit entry records which posting a rule wrote, and a reversing entry records which posting it undoes. Identifying a posting by its position inside a transaction would break all three, and break them *silently* — reordering happens constantly and innocently, whenever a query returns rows in a different order, a decoder rebuilds a slice, or an importer sorts for display. There is no error and no failed constraint, only a cost basis quietly attached to the wrong acquisition and found out a tax year later.

It is also what makes §5.3 workable. Corrections are reversing entries rather than edits, and a reversing entry needs something stable to reverse; "the second line of transaction T" is not stable enough to build an immutable history on.

Consequences, all enforced:

- Posting identities are unique across the **whole journal**, not merely within a transaction, so a reference finds exactly one posting.
- `Lot.OpenedBy` names a `PostingID`, not a `TransactionID`. A transaction can acquire two things at once, and "which line was this lot" then has no answer. `Journal.PostingOwner` widens a posting back to its transaction, so the two facts cannot disagree.
- `Journal.Posting` resolves a posting by identity from anywhere in the book.
- A property test reorders the postings inside a transaction under every permutation rapid can find and asserts that every lot still resolves to the same line, by account and amount rather than merely by identity.

**Identities are validated as canonical UUIDv7 inside the domain**, not only at the API boundary. §5.6 already required the shape; leaving it unenforced would have made it a convention, and a rule engine or importer minting `lot-1` would have gone unnoticed. `ValidateID` is exported so the edge can reject a malformed identity where the error can still become a useful response. Lowercase only: uniqueness here is exact string comparison, so one identity in two spellings would look like two identities. The cost is that identifiers imported from another system have to be re-minted rather than carried across, which lands on the M2 importers.

**JSON encoding for `Money`, `Date` and `Rate` lives on the domain types**, even though §6 puts DTOs in `api`. §4.5 is a money rule rather than a transport preference, and putting the encoding on the type means no handler anywhere can serialise an amount as a JSON number. `encoding/json` involves no I/O, so purity is intact.

**`ConversionPostings` is a method on `AccountTree`,** not a free function. The equity requirement in §5.1 cannot be checked without the tree, and a builder that could not verify its own precondition would leave the rule to review again.

**It does not check that `Sold × Rate == Bought`.** Verifying that needs a tolerance, because a rate almost never lands on a whole number of smallest units — and a tolerance is exactly what §5.1 refuses. The rate is recorded as evidence, not graded. A caller wanting the two to agree derives one side with `Registry.Convert` and `Rat.Round`.

**FIFO is the only lot policy in core.** Which policies a taxpayer may use is a country-specific question, so the alternatives belong in Country Packs. Lot ordering breaks ties on identity after the date, because without that the same disposal could compute a different gain on different runs.

**Immutability is structural, not documentary.** Every domain type keeps its fields unexported and copies any pointer it holds in both directions. `Lot.Consume` returns a reduced lot; `Journal.Add` returns a new journal. A property test tries every route into the internals — the source `big.Int`, the one `Amount()` hands back, the postings slice — and asserts none of them reach anything.

#### What went wrong, and the rule it produced

**`go mod tidy` raised the module's Go floor from 1.22 to 1.25.** Adding `testify` pulled `gopkg.in/yaml.v3`, whose *tests* use `gopkg.in/check.v1`, which uses `kr/pretty`, which needs `rogpeppe/go-internal`. Tidy resolved that to a version requiring Go 1.25 and rewrote the directive without being asked. Nothing in that chain is ever executed by anything Nusa ships. Fixed by pinning `go-internal` to v1.12.0 and running `go mod tidy -go=1.22`; `rapid` needed the same treatment, since v1.3.0 requires Go 1.23 and v1.1.0 does not.

This is the M0 tools lesson recurring through a door that was not closed: M0 stopped a *linter* from setting the floor, and a transitive test dependency did it instead.
> **Rule.** Always `go mod tidy -go=1.22`. Read the directive afterwards. See §3.

**CI would not have caught it.** `GOTOOLCHAIN: auto` was set for the whole workflow, so a bumped directive would have made Go download the newer toolchain and every job would have gone green while the promise in the README quietly stopped being true. `GOTOOLCHAIN` is now `local` everywhere except the two steps that build `tools/`, and `go-build` compares the directive against the version CI installs.
> **Rule.** A guard that the thing it guards can satisfy by itself is not a guard. Ask what the check would do if the rule were already broken.

**`CLAUDE.md` was in `.gitignore`.** It had been grouped with `.dev/` as a local working note during M0, which meant the one normative document in the project — and §13's milestone record, written to be read by whoever comes next — would never have reached a contributor. Only `.dev/` is ignored now.
> **Rule.** `.gitignore` decides what other people can see, not merely what is convenient locally. Anything normative is committed.

**A verification criterion was met in code but not literally.** `grep -rE "float64|float32" internal/ledger/` returned five matches after the package was written — every one a comment explaining why floats are banned. The prose was reworded to say "IEEE 754 double" so the check returns nothing.
> **Rule.** If a check is stated as a command, the command's output is the criterion. Rewrite the code to satisfy it or change the check deliberately; do not report "passes apart from the false positives". Recorded in §11.

#### Deliberately deferred

| Deferred | Lands in |
| --- | --- |
| Persistence for every type here: schema, repositories, idempotency keys, audit log, OpenAPI | M2 |
| Splitting an amount N ways without losing a sen. Needed by envelope allocation, not by the ledger | M4 |
| Lot selection policies other than FIFO, price providers, XIRR, unrealised gain reporting | M6 |
| Valuing a multi-commodity balance as one figure. Needs a rate and a date, which is a reporting decision above this package | M6 |
| The full ISO 4217 table. Core ships the currencies a self-hoster is likely to hold; packs register the rest | M8 |
| Country-specific commodity scales — IDX whole shares, mutual fund units | M8 |
| Reversing entries and tombstones as first-class operations. The immutability that makes them the only option is in place; the helpers are not | M2 |

### Design authority — `DESIGN.md`

A design system document, `DESIGN.md`, was added at the repository root and made
the single authority for how Nusa looks. §8 was split at the same time: §8.1
points at `DESIGN.md` and holds no values, §8.2 holds the functional floor that
binds any design system. The frontend moved from the dark "Frutiger Aero" palette
that had lived in §8 to the light system `DESIGN.md` specifies.

#### Why the split, rather than one document

`DESIGN.md` and §8 briefly both claimed authority over the same subject, and a
line-by-line comparison found them disagreeing on nearly everything: light
versus dark, every colour, three of four radii, drop shadows versus accent
glow. Two normative documents is worse than either one alone, because the
answer to "what colour is an error" then depends on which file you opened.

They were not merged, because they are not the same kind of statement.
`DESIGN.md` says what Nusa looks like — replaceable, a matter of taste, and
owned by whoever designs. §8.2 says what any interface here must do to remain
usable for someone reading numbers: contrast, numeric alignment, reduced
motion, RTL, focus. Those survive a redesign. Collapsing them into one document
would have made the floor look like a preference, and preferences lose
arguments to taste.

#### Two values per status colour

Each status colour carries a FILL and a CONTENT value, and this is the part of
the system most likely to be misunderstood as redundancy.

A colour bright enough to read well as a large filled shape is almost never
dark enough to carry 12px text. `#EAB308` is a good warning fill and, as a
warning label on white, sits at 1.92:1 against a floor of 4.5:1. One value per
colour forces a choice between a washed-out interface and unreadable labels.

So: FILL for backgrounds and purely decorative shapes; CONTENT — mandatory —
wherever the colour carries text, an icon, a status dot or a chart key. The
test is whether removing it would lose information.

#### Borders: split by role instead of darkened

WCAG 1.4.11 asks for 3:1 where a boundary is the **sole** identifier of a
control. Neither border value reached that. Darkening them to roughly `#78889B`
would have satisfied the checker and changed the character of every surface in
the product.

The requirement was met from the other side instead. `--border-subtle` is
decorative only; `--border-strong` marks control boundaries; and the binding
rule is that **every interactive control carries an indicator besides its
border** — a distinct fill, a permanently visible label, or an icon. Auditing
the components against that rule found three that had been relying on their
border alone: inputs, unchecked checkboxes and radios (all three have a fill
identical to the card behind them), and filter chips (fill identical to the
page). Each now requires a permanently visible label, which is what actually
identifies them.

The focus ring is exempt from this reasoning and holds 3:1 unconditionally.
Focus is the only marker of keyboard position, so there is no second cue that
could rescue it.

#### Failures the checks found

Three real defects, none of them visible by eye:

- **The destructive button was unreadable.** White on `#EF4444` is 3.76:1. It
  now fills with the error CONTENT value, at 6.47:1.
- **The focus ring vanished on dark fills.** A ring in `--focus-ring`
  (`#0F172A`) on the primary button fill (`#0F172A`) is 1:1 — invisible exactly
  where a keyboard user needs it most. `--focus-ring-inverse` exists for those
  surfaces.
- **`--text-secondary` failed on five surfaces**, not one: the sunken surface
  and four of the five status chip surfaces. The first proposal was to keep
  `#64748B` and forbid it on the one surface then known to fail. That was
  wrong twice over — it would have left four failures standing, and a
  constraint that depends on a contributor remembering it produces no error
  when broken, only text that is slightly harder to read. It was darkened to
  `#5A697F` instead, which clears every surface.

#### The lesson: prove a guard by breaking it

Two guards were added: `lint:tokens` (rejects raw colours, fonts, lengths and
third-party asset hosts outside the token files) and `test:contrast`
(recomputes every pairing from the shipped tokens, at thresholds that differ by
role). Both ran clean on first try.

One of them was dead. The check for arbitrary lengths in a `className` never
fired: its regex did not match `gap-[13px]`, and it only looked at
`className="..."` attributes, so every class list built as an array and joined
— which is how the language switcher is written — was invisible to it. It
reported success on a codebase it was not reading.

Nothing but a deliberate violation would have found that. The negative tests
were originally scheduled after the guards were installed and reported passing;
running them at installation time instead is what caught it.
> **Rule.** A guard is not installed until you have watched it fail. Introduce
> the violation it exists to catch, confirm the failure, then revert. "The
> check passes" is not evidence the check works — a check that reads nothing
> also passes. This is the M1 lesson about `GOTOOLCHAIN` in a second costume.

A margin band was added for the same reason. A pairing that clears its
threshold by 0.08 is one edit from failing and nothing about the number says
so, so pairings passing below 4.8:1 (or 3.3:1 where the floor is 3) are
reported as warnings without failing the build.

#### Where it stands

61 pairings verified, none failing. The tightest is
`--status-success-content` on `--surface-sunken` at **4.58:1** against a floor
of 4.5. All three warned pairings are that same colour, which makes success the
weakest value in the palette and the first thing to re-verify if it moves.

Fonts are self-hosted in woff2 — Plus Jakarta Sans 500/600/700, DM Sans
400/500, Fira Code 400, `latin` and `latin-ext` only. Nothing is fetched from a
font CDN, because a font request hands the reader's IP address to a third party
and defeats the point of self-hosting (§10). Adding a non-Latin language will
require adding its subset before that translation ships.

#### Deliberately deferred

| Deferred | Lands in |
| --- | --- |
| Component primitives for everything `DESIGN.md` specifies — buttons, inputs, chips, lists, checkboxes, radios, tooltips. Only the health card and language switcher are built | M3 |
| The settings UI that flips `data-effects="reduced"` and `data-density="dense"`. Both attributes and their token overrides exist; nothing toggles them | M3 |
| Dark theme, as a derived block redefining the same semantic token names | Unscheduled |
| Automated checks for the non-contrast parts of §8.2 — logical properties, tabular numerals, touch target sizes | M3 |
| Playwright coverage of the 360px floor and the table-to-card reflow | M3–M4 |

### Repository move — `github.com/GaffaQ/Nusa`

The project moved from the placeholder `github.com/nusa-app/nusa` to its real
home. Both modules were renamed, along with every import, the depguard rules in
`.golangci.yml`, and the goimports local prefix.

**The capital N is load-bearing.** Go treats a module path as case-sensitive.
GitHub does not treat a URL that way, so `github.com/gaffaq/nusa` resolves
perfectly in a browser and then fails to match what the module proxy recorded
for `github.com/GaffaQ/Nusa`. The two disagree only once someone else tries to
depend on the module, or once the proxy caches the first spelling it saw — long
after whoever typed the lowercase version has moved on. The path is never
normalised.
> **Rule.** A module path is copied from the repository exactly, including
> case. Any tooling that lowercases it is wrong for this field.

The rename does not touch the product name. `internal/brand/brand.go` remains
the only place the name is spelled in Go source, and §3 now states the
separation directly rather than implying it: renaming the product does not move
the module, and moving the repository does not rename the product.

The depguard rules were re-verified rather than assumed: importing
`internal/store` into `internal/ledger` was confirmed to fail against the new
path, then reverted. A path rename is exactly the kind of change that can leave
a rule matching nothing while still reporting success.

#### `brand.RepositoryURL` and the three constants

`internal/brand/brand.go` holds three constants that look alike and are not.
`Name` and `Slug` are the product's identity and move only when the product is
renamed. `RepositoryURL` is where the source lives and moves when the
repository does. The rename briefly left `RepositoryURL` pointing at a
repository that no longer existed, advertised through the health endpoint,
because "do not touch brand.go" was read as covering all three. The file now
says which is which, at the top of the const block, so the next reader does not
have to reconstruct the distinction.

#### Reconciling with the remote's initial commit

GitHub had created the repository with an `Initial commit` containing an
AGPL-3.0 `LICENSE`, so the two histories had diverged before the first push.
Resolved by rebasing local work onto it rather than force-pushing over it.

The `LICENSE` conflict is worth recording because the two files were nearly
identical: both the genuine AGPL-3.0 text, differing only in where two lines of
the closing "How to Apply These Terms" appendix wrapped. The local version was
kept.

Note for anyone resolving a rebase conflict here: during a rebase the sides are
inverted. `--ours` is the branch being replayed *onto* (the remote), and
`--theirs` is the commit being replayed (the local work). Taking `--ours` to
mean "our work" is the natural reading and the wrong one.
> **Rule.** Compare conflicting files by their git object hashes, not by
> diffing working-tree files. On Windows the working tree is CRLF and `git
> show` emits LF, so a byte-identical file diffs as entirely different and a
> correct resolution looks like a failed one.

#### What the licence structure actually is

Verified after the rebase, and it does not match what is sometimes assumed:

- `LICENSE` at the root is AGPL-3.0. It is the **unmodified** upstream text:
  the appendix still carries the unfilled `Copyright (C) <year>  <name of
  author>` placeholder, and no personal or project copyright line has ever been
  added. The only copyright notice in the file is the FSF's on the licence text
  itself.
- `internal/ledger/LICENSE` is MIT, `Copyright (c) 2026 Nusa contributors`.
- **There is no `LICENSING.md`.** Nothing in the repository explains the split
  between the AGPL core and the MIT ledger except §6 of this file.
- SPDX headers are consistent: 27 MIT-headed Go files, all inside
  `internal/ledger`, none outside it; 23 AGPL-3.0-only elsewhere.

The unfilled AGPL copyright line stays that way deliberately: the `LICENSE`
files are the unmodified upstream texts, and editing licence text is how
automatic licence detection breaks. Copyright is asserted per file through the
SPDX headers instead. `internal/ledger/LICENSE` attributing to *Nusa
contributors* rather than to one person is likewise deliberate — as soon as
there is a second contributor it is simply what is true, and it never needs
updating.

#### `LICENSING.md` was missing from M0 until now

The two-licence split shipped in M0 with nothing in the repository explaining
it. §6 of this file stated it, but this file is documentation for people
*working on* Nusa, not for someone deciding whether they may use the ledger in
their own project — which is the entire point of making it MIT.

It survived M0's verification because M0's checklist never mentioned it. Every
item on that list was checked and passed; a licensing document was not an item,
so its absence produced no failure and no warning. The gap was found only when
a later session went looking for a file it had been told existed.
> **Rule.** A verification list only proves the things on it. Absence of
> failures is not evidence of completeness, and the things most likely to be
> missing are the ones nobody thought to check for. When adding a deliverable,
> add its check at the same time.

`LICENSING.md` now carries the split, the reasoning behind where the line is
drawn, and the SPDX convention — including its one exception: the four
sqlc-generated files in `internal/store` carry no SPDX header, because
`sqlc generate` would strip a hand-added one and CI's regeneration-drift check
would then fail. They are AGPL-3.0-only as part of `internal/store`. All 50
hand-written Go files do carry a header.

#### Environment limitation: `make` has never run here

`make` is not installed on the development machine this work was done on. Every
report of "tests pass" or "lint passes" in this session refers to the commands
*behind* the Makefile targets — `go test -race ./...`, `./bin/golangci-lint
run`, `npm run build` — not to `make test` or `make lint` themselves.

**The Makefile is therefore unverified.** M0 already found one bug in it that
only running the real target could expose (a prerequisite named `node_modules`
that never matched `web/node_modules`, causing a full reinstall before every
test run). A second bug of that kind would still be invisible here.

CI runs the real targets, so the first push is also the first genuine test of
the Makefile. Treat a CI failure in that area as expected information rather
than as a surprise.
> **Rule.** Report the command that was actually run. "The commands behind the
> target pass" and "the target passes" are different claims, and the gap
> between them is exactly where the M0 Makefile bug lived.

#### Environment limitation: line endings on Windows

`core.autocrlf=true` on the development machine and no `.gitattributes` to
override it. Git therefore rewrites every text file to CRLF on checkout and
back to LF on commit.

The committed bytes are correct — all 54 Go blobs in `HEAD` are LF, and gofmt
accepts every one of them, so CI on Linux is unaffected. But **`golangci-lint
run` fails locally on every Go file after any operation that re-materialises
the working tree**, such as the rebase that reconciled with the remote's
initial commit: gofmt sees CRLF and reports the whole file as unformatted,
while `git status` shows nothing modified because git converts it back before
comparing.

This is confusing in a specific way: the lint failure looks like a real
regression, `git status` insists nothing changed, and running the formatter
"fixes" files that were never broken. golangci-lint's cache makes it worse by
reporting an arbitrary subset rather than all of them.

**Resolved.** `.gitattributes` now pins `text=auto eol=lf`, lists the
extensions this project actually uses rather than leaving them to git's guess,
and marks binary formats — fonts above all — so eol conversion never touches
them. `git add --renormalise .` changed nothing, which confirmed the stored
blobs had been LF all along; the fix was to the working tree, not the history.
After re-materialising the checkout, all 54 Go files are LF and
`golangci-lint run` passes locally with a cleared cache.
> **Rule.** Before treating a formatter or linter failure as a code problem on
> Windows, check whether the committed blob differs from the working tree only
> in line endings. Verify against `git show HEAD:<file>`, never against the
> file on disk.


### M2 split into M2a and M2b

Planning decision, taken before any M2 code was written. The full
implementation record for M2a is appended when M2a completes; this entry exists
because the reasoning is about *sequencing* and is worth having on record
independently of how the work turns out.

One label was holding two milestones: mapping the domain onto tables, and
building authentication. Both are large, both carry risk, and neither teaches
anything about the other.

| | Scope |
| --- | --- |
| **M2a** | Schema, repositories, atomic writes, idempotency, audit log, integration tests |
| **M2b** | Authentication, sessions, TOTP, rate limiting, REST API, OpenAPI |

**Persistence goes first.** `internal/ledger` has been proved correct in memory
and nowhere else, and writing something down and reading it back is the only
way to find out whether the model survives contact with storage. If anything in
M1 is wrong, M2a is what finds it — and finding it before an authentication
layer sits on top is far cheaper. Authentication, by contrast, teaches nothing
about the ledger.

`idempotency_keys` and `audit_log` stay in M2a rather than moving to M2b with
the HTTP layer. Both are part of the persistence model: §5.6 is a property of
writes, not of requests, and the importer, the rule engine and the scheduler
all replay writes with no HTTP anywhere in sight. A key enforced only in
middleware is a key that three future callers bypass.

#### Balances: SQL aggregation, with no cache at all

Three approaches were weighed: aggregate on demand in SQL; cumulative monthly
checkpoints plus a partial-month delta; a running current-balance cache
maintained in the writing transaction. The first was chosen.

§5.2 already calls a cached balance an optimisation, and there were no
measurements to justify one. More decisive: **the aggregation query is the
oracle the other two need.** Drift detection for a checkpoint table or a
balance cache *is* the plain `SUM` over postings. Building it first means the
checker exists and is tested before there is anything to check; building the
cache first means writing the cache and its checker in the same breath, which
is how a guard nobody has watched fail gets installed.

The schema shape is identical under all three, so nothing is foreclosed: a
checkpoint table or a balance cache is a later `CREATE TABLE`, touching no
existing row.
> **Rule.** When one candidate design is the definition the others are
> approximations of, build the definition first. It is also their test.

A benchmark with an explicit threshold ships alongside, so that revisiting the
decision is triggered by a number rather than by remembering to ask.

#### Docker was not a limitation after all

The previous session recorded that the Docker daemon was unreachable, and this
one was told to record it as a fourth environment limitation. It is not one.
The first `docker info` failed because Docker Desktop was still starting —
roughly thirty seconds elapsed between the process appearing and the named pipe
being served. A retry succeeded from both shells, and every M2a verification
ran against a real PostgreSQL 16.14.

Worth knowing rather than worth warning about: the daemon needs a moment after
launch, and Postgres is published on **55432**, not 5432, because `POSTGRES_PORT`
in `.env` was moved during M0 to avoid a local conflict.
> **Rule.** Distinguish "unavailable" from "not ready yet" before writing either
> one down. A limitation recorded from a single failed probe outlives the
> condition that produced it, and every later session pays for the caution.

### M2a — Persistence

The domain written down and read back: schema, repositories, atomic writes,
repository-level idempotency, audit log, and integration tests against a real
PostgreSQL. `internal/ledger` is byte-for-byte unchanged, which was the point.

Six migrations, ten tables, 38 check constraints, two constraint triggers, 31
integration tests.

#### Balances are summed in SQL, and nothing is cached

Three designs were weighed before any code was written — aggregate on demand,
cumulative monthly checkpoints, a running balance maintained in the writing
transaction. The first was chosen, and the reasoning is recorded above under
*M2 split into M2a and M2b*. The short version: the aggregation query is the
definition the other two would be approximations of, and their drift detector,
so building it first means the checker exists before there is anything to
check.

**The measurement, on 40 accounts with a realistic spread, busiest account
measured** (Docker Desktop on Windows, warm cache, so read these as a shape
rather than a promise):

| postings | in the account | `Balance` p95 | `BalanceAsOf` p95 | `SubtreeBalance` p95 |
| ---: | ---: | ---: | ---: | ---: |
| 10.000 | 500 | 1,09 ms | 0,85 ms | 2,34 ms |
| 100.000 | 5.000 | 3,16 ms | 2,88 ms | 16,45 ms |
| 500.000 | 25.000 | 8,52 ms | 5,33 ms | 76,34 ms |

For calibration: a household posting fifty transactions a month for twenty
years writes roughly 24.000 postings across the whole book. The 10.000 row is
already past a twenty-year ceiling for a single account.

> **Trigger.** Revisit the decision not to cache when **`BalanceAsOf` for one
> account exceeds 50 ms at p95**, or when **any single account holds more than
> 200.000 postings**. Both are asserted by
> `TestBalanceLatencyStaysWithinItsBudget`, which fails the build rather than
> printing a number nobody is obliged to read. A cache added later is a
> `CREATE TABLE` that touches no existing row, and this query is what would
> check it for drift.

**A subtree balance is roughly nine times slower than the same account's own
balance at 500.000 postings, and the gap widens with volume.** The plan is a
healthy index-only scan with zero heap fetches at 50.000, so this is a scaling
effect that has not been isolated. It is guarded separately and loosely at
150 ms — a regression guard, not a certification — and left as an open question
for M6, where reporting actually leans on subtree sums.

#### What the round-trip property test found

Random transactions — one to three commodities, two to four lines each, amounts
drawn as bytes and read as `big.Int` so no monetary value ever passes through a
double, exact fractional rates, lots already partly consumed — written to
PostgreSQL, read back, and compared. 3000 checks, about 123.000 balance
comparisons, 119 seconds.

The central assertion is that **SQL and the domain agree**: a `ledger.Journal`
is built from the same transactions and every balance query is compared against
it, `SumPostings` included. Two independent implementations of one definition,
run over the same data. If they ever diverge the SQL is wrong, because §5.2
makes `Journal` the definition.

It found one real mismatch, and it was not in the money.

**A Go string may contain NUL; a PostgreSQL `text` value may not.** A generated
payee containing U+0000 failed on the eighth case with `SQLSTATE 22021`, an
error naming neither the field nor the fix. A probe narrowed it to exactly one
code point: control characters, newlines, emoji, combining marks and bidi
overrides all round-trip untouched.

The store now refuses NUL by name, before writing, on payee, memo, posting memo
and account name. Not stripped — quietly mutating what someone typed is what
this project does not do, and a payee that silently loses a character no longer
matches the one on the bank statement.

Whether that belongs in the domain instead is genuinely open. NUL is rejected
by JSON, by C string APIs, by filenames and by HTTP headers, so "text a ledger
can hold" is arguably a domain rule that PostgreSQL merely noticed first. It
stayed in the store because M2a froze the domain; M2b unfreezes it for
reversing entries and is the natural place to revisit.
> **Rule.** A generator that only produces values someone thought of tests only
> what someone thought of. The one defect in the persistence layer was in a
> string field, found by a fuzzer, in a value no reviewer would ever have
> written by hand.

#### The deferred balance trigger does not like bulk loads

§5.1 is enforced twice: by `NewTransaction` before the write, and by a
`DEFERRABLE INITIALLY DEFERRED` constraint trigger at COMMIT. The trigger is
what still holds when an importer or a rule engine writes without coming
through the repository, and its scope is stated in the migration and bounded to
§5 alone.

It has a cost that only appears at volume. Every header and every posting
queues an after-trigger event held until COMMIT, and the queue does not scale
linearly: **300.000 events commit in about eight seconds; 750.000 events in a
single transaction had not finished after thirteen minutes.**

The fix is not to weaken the trigger. It is to load in chunks — a transaction's
header and all of its lines inside one chunk, which is the only thing the
constraint requires. The benchmark fixture uses 5.000 transactions per chunk
and loads 500.000 postings in about twenty seconds.
> **Rule.** The importer must batch. A single database transaction wrapping an
> entire import will appear to hang at COMMIT, with no error and no progress,
> which is the worst failure shape available.

#### Decisions worth knowing

**The schema precedes the operations, deliberately.** `reverses_id`,
`reversal_kind` and `reverses_posting_id` exist; nothing writes them yet, and
`SetLotRemaining` exists as a query with no operation behind it. Reversing
entries and lot consumption are accounting operations, not persistence, and
putting a domain-shaped builder in `store` would have set a precedent that
erodes §6 — the next session would cite it as proof that domain logic may live
in the store when the reason is good enough. Both move to M2b, where the domain
freeze is lifted explicitly and only for them. Columns are cheap now and
expensive to add to a populated table later; operations are not.

**Every write requires an actor.** `idempotency_keys.actor_id` is NOT NULL,
because a key is scoped per actor: two people may pick the same key and neither
may receive the other's result. Rules, imports and the AI layer all act on
behalf of someone. If Nusa ever grows a genuinely ownerless rule, this is the
decision to reopen — `audit_log.actor_id` is already nullable for exactly that
shape, and the two would then disagree.

**An expired idempotency key is reclaimable,** not merely swept. Without that
the TTL would mean nothing for correctness and only something for table size,
which is a TTL that misleads.

**The fingerprint covers the whole request,** not just the entity id. Same key
with one digit changed is refused rather than replayed: the caller believes it
is retrying something it is not.

**A transaction may hold at most 32.768 postings,** the ceiling of the ordinal
column. No real transaction approaches it; the check exists because the
alternative is a silent wrap to a negative ordinal, refused by a constraint
whose message names neither the cause nor the fix.

**Integration tests carry no build tag.** A tagged suite is one that a green
`go test ./...` says nothing about, and "the tests pass" would quietly come to
mean "the tests that ran passed". Docker missing is a loud failure. The cost —
contributors need Docker running — is documented in `CONTRIBUTING.md`, not only
here, because someone who cannot run the tests in their first minute leaves.

#### Guards, each watched failing

Every guard installed this milestone was broken on purpose at installation
time, per the rule from the design-system work. In order:

- **Seeded commodities vs `StandardRegistry`** — JPY's scale changed 0 to 2:
  fails on scale. ETH removed from the seed: fails on the count.
- **Posting order** — read back by id instead of ordinal: fails after 0 tests.
- **`BalanceAsOf` boundary** — `<` instead of `<=`: fails after 0 tests.
- **Numeric exactness** — `numericTo` ignoring the exponent: fails after 2
  tests, and informatively. It means pgx really does return a non-zero exponent
  for some `numeric(40,0)` values, so that branch is load-bearing rather than
  defensive.
- **NUL rejection** — validation removed: the raw `SQLSTATE 22021` leaks
  through, exactly as it did before the guard existed.
- **The balance trigger** — eighteen violations driven straight through psql,
  bypassing the repository and the domain entirely.

One guard was found incomplete by its own numbers rather than by a deliberate
break: the latency test originally asserted a budget on `Balance` and
`BalanceAsOf` but not on `SubtreeBalance`, and let a 76 ms p95 pass unremarked.
> **Rule.** Breaking a check proves it can fail. It does not prove it covers
> everything it should.
>
> So coverage is decided *before* the guard is written, not inferred from it
> afterwards: **list what the guard must cover, then implement it, then break
> each item on the list.** A guard written first and audited later is audited
> against itself, and the thing it forgot to measure is exactly the thing
> nobody thinks to look for. Reading the assertions back and comparing them
> against what the test actually measures is the last step, not the first.

#### Environment

Docker Desktop stopped by itself three times during this milestone — once
mid-test-run, twice between runs — leaving no engine pipe and no processes.
Each restart recovered cleanly. Worth knowing rather than worth working around:
the named pipe reappears several seconds before the daemon answers, so wait on
`docker info` succeeding, not on the pipe existing.

#### State after M2a

The ledger is persistent. A transaction built in the domain can be written,
read back byte-for-byte, and summed into a balance that SQL and
`ledger.Journal` agree on — proved over 3000 random cases rather than asserted.
Writes are atomic, idempotent at the repository, and audited with an origin.
The schema refuses an unbalanced transaction even when the writer never touched
Go.

`internal/ledger` is unchanged. That was the milestone's real question — whether
the M1 model survives contact with storage — and the answer is that it did, at
the cost of one store-level rule the domain does not know about (NUL in text).

What does **not** exist: any HTTP surface beyond `/healthz`, any
authentication, and any way for a person to reach the ledger. M2a made the
ledger storable; it did not make it reachable.

**Three debts open the domain freeze, and they open it once.** M2b lifts it
deliberately and only for these:

1. **Reversing entries** — `transactions.reverses_id` and `reversal_kind` are
   written by nothing. §5.3 makes a correction a new reversing transaction, and
   the builder for it is domain logic that had nowhere to live in M2a.
2. **Tombstones** — the same mechanism with `reversal_kind = 'deletion'`. A
   "delete" is an append, never a mutation, and nothing implements it yet.
3. **Lot consumption** — `SetLotRemaining` exists as a query with no operation
   behind it. FIFO selection lives in `ledger.ConsumeFIFO`; wiring a disposal
   through it and writing the reduced lots back is the missing half.

Plus the open question the property test raised: **whether NUL rejection
belongs in the domain** rather than in the store. It is the same door, so it
goes through it at the same time.

Everything else M2b needs — `sessions`, credential columns on `users` — is
additive schema that touches no existing row.

#### Deliberately deferred

| Deferred | Lands in |
| --- | --- |
| Reversing entries and tombstones as operations. Schema, constraints and links are in place; the builders are not, and the domain freeze lifts for them | M2b |
| Lot consumption. `SetLotRemaining` exists as a query with no operation behind it | M2b |
| Whether NUL rejection belongs in the domain rather than the store | M2b |
| `sessions`, and credential columns on `users` by ALTER rather than CREATE | M2b |
| `fx_rates`. No domain type and no consumer yet, so its shape would be a guess. Per-posting rates that §5.4 requires are already stored | M6 |
| Why a subtree balance degrades faster than a single-account balance | M6 |
| Cursor pagination, DTOs, OpenAPI | M2b |

### M2b Phase 0 — the domain freeze, opened once and closed again

The three debts M2a left against `internal/ledger`, plus the open question it
raised, done together in one deliberate opening of the freeze and before a
single line of HTTP exists. **The freeze is back on: `internal/ledger` is
closed to change again, and M2b's remaining work — auth, sessions, TOTP, rate
limiting, REST, OpenAPI — adds nothing to it.**

Sequencing them ahead of authentication was the point. If the M1 model were
wrong about reversal, this is where it shows, with no DTO shapes and no
handlers built on top of the answer.

#### NUL moved into the domain, and the store keeps checking anyway

`NewTransaction`, `NewPosting` and `NewAccount` now refuse U+0000 in payee,
memo, timezone and account name. "Text a ledger can hold" is a fact about the
ledger, not about PostgreSQL — a value carrying NUL survives neither JSON, nor
a C string API, nor a filename, nor an HTTP header — and a rule enforced only
by the current storage engine is one a future importer writes around without
noticing. Refused, never stripped: silently dropping a character is how a payee
stops matching the statement it was copied from.

This is the one change of the four that *narrows* what was previously accepted,
which is why it was decided first. Doing it after the reversal builder would
have meant writing that builder's validation twice.

The store's own check stays, and the redundancy is deliberate — the comment on
`nulNotAllowed` says so, at length, so a later session does not tidy it away.
It is not, however, the same shape of redundancy as the deferred balance
trigger, and the difference is worth being exact about. There are three layers:

| Layer | Fires when | Proved by |
| --- | --- | --- |
| `internal/ledger` | always; nothing carrying NUL can be built | breaking each of the five call sites in turn |
| `internal/store` | only if the domain rule is weakened | removing the domain check: the store's error surfaced, not a SQLSTATE |
| PostgreSQL | when Go is bypassed entirely | a direct `INSERT` through the pool, refused with 22021 |

The middle layer is unreachable through its own signature today, because a
`ledger.Transaction` cannot carry a NUL. That makes it a regression guard
rather than a bypass guard, which is a smaller claim than the trigger's and is
now stated as such in the code.

#### One reversal builder, and the rate it must not recompute

`ledger.Reverse` serves corrections and tombstones through a `Kind` parameter.
Two builders would have been two places for §5.3 to drift apart.

**Every posting's `Rate` is carried across verbatim.** This is the load-bearing
detail. A reversal that priced itself at today's rate would leave the pair
failing to cancel by however far the rate had moved — a fabricated gain nobody
booked — and §5.4's promise that last year's report still says what it said
would be gone. There is a hand-written test at a rate two years stale, and a
property test that asserts it over every generated transaction.

Three smaller decisions:

- **The new posting identities arrive as a map keyed by the original posting's
  identity**, never as a slice in posting order. §5.7 forbids pointing at a
  posting by position, and a reordered slice would attach a reversing line to
  the wrong original with no error anywhere. A property test reverses a
  transaction and its own permutation and compares the results by what each
  line answers.
- **The date is required, never defaulted.** Booking a reversal today leaves
  last year's report intact; booking it on the original's date rewrites that
  period. Which is right is an accounting decision, and a domain that guesses
  is a domain that silently moves money between periods.
- **A reversal may itself be reversed.** Undoing a deletion is a real thing
  people do, and refusing it would be a policy the domain has no business
  inventing. `reverses_id` being UNIQUE still stops the same entry being
  reversed twice.

The audit log now distinguishes `create`, `correct`, `delete` and `dispose`.
All four are appends; they are not the same event to a person reading their own
history, and "who deleted this" should be answerable by filtering the log
rather than by joining the ledger back onto itself.

#### `lot_consumptions`, and the field that would have been lost

The gap was not on anyone's list: a disposal reduced `lots.remaining_amount`
and left no record of having done so. The running figure stayed correct and the
history behind it was gone — which acquisitions a sale drew on, from which
dates, at what price. That is unreconstructible rather than inconvenient, since
FIFO depends on what was open at the moment of the disposal and later activity
changes that. It was also the one place the ledger quietly overwrote itself,
inside an UPDATE, in a book §5.3 makes append-only.

**`Consumption.Basis` is a `ledger.Rat`, not a `Money`, and that is what the
schema had to be built around.**

Anyone meeting `basis_num` and `basis_den` will ask why this is not simply a
`numeric` like every other amount. One line answers it: **sell 0,1 BTC out of a
0,3 BTC lot that cost Rp 1.000.000, and the basis is 100.000.000 ÷ 3 minor
units — 33.333.333,33… — which no `numeric(40,0)` can hold.**

Rounding it to 33.333.333 would be rounding *in the middle* of a calculation,
invisibly, once per consumed lot, and the rounded pieces would then not add
back up to the million that was actually paid. §4.6 puts rounding at the last
step only and §4.7 says anything that cannot stay a whole number of smallest
units stays a `Rat` until `Round` is called on it deliberately. A stored basis
is not the last step: a realised gain sums several of these and rounds once, at
the end.

So the basis is two `numeric` columns, reduced, with a positive denominator —
exactly as a rate is stored, for exactly the same reason a rate cannot be a
decimal. Nothing else in `Consumption` loses anything on the way to a row.

- **The primary key is `(posting_id, lot_id)`**, with no identity minted for
  it. §5.7 asks for an identity for everything that can be pointed at, and
  nothing points at a consumption: it is the join between a line and a lot,
  both of which already have one.
- **Consumption order is not stored.** FIFO order is `(opened_on, id)` on the
  lots, so it is derivable; more fundamentally, the order a policy picked lots
  in is a trace of the algorithm, not a fact about the disposal. The set of
  (lot, quantity, basis) determines the gain whichever order they came in.
  *Which* policy chose them is a separate and real question once Country Packs
  bring policies other than FIFO, and is deferred rather than guessed at.
- **Two composite foreign keys** hold the commodity columns to the lot's own,
  the same arrangement `postings` uses for its copy of `txn_date`.
- **Lots are read `FOR UPDATE`** inside the writing transaction. Without it,
  MVCC lets two concurrent disposals both read the original figure — a plain
  read is never blocked by a row lock — and both consume the same units.

`ledger` needed nothing new for any of this: `ConsumeFIFO`, `Lot.Consume` and
`Consumption` were already sufficient. The whole third debt was store work.

#### What went wrong, and the rules it produced

Four guards were installed, passed, and were wrong. Every one was caught by
breaking it, and none would have been caught by reading it.

**A property test asserted something it never saw.** The reversal property test
checked that rates are carried verbatim — over a generator that attaches no
rates to anything. Deleting the line that carries the rate through `Reverse`
failed only the single hand-written test. Fixed by drawing a rate onto every
line first; the break then failed the property test too.
> **Rule.** An assertion inside a property test is only as good as the
> generator feeding it. Before trusting one, ask whether the generated data can
> even contain the thing being asserted about — and prove it by breaking the
> code the assertion covers and watching *that* test fail, not a neighbour.

**A test isolated less than it claimed.** "Two different corrections are not
taken for a replay" passed with the reversal link removed from the fingerprint,
because the two originals also differed in payee. Any two reversals built by
`Reverse` differ in their per-line links as well, so isolating the
transaction-level field required building two reversals by hand with no
line-level links at all — identical in every byte except what they undo.
> **Rule.** When a guard covers one field, construct the case so that field is
> the only difference. A test that would pass for three different reasons is a
> test of none of them.

**A concurrency test proved nothing.** Two goroutines calling `SaveDisposal`
passed with the row lock removed: they rarely collide, and when they do not,
the assertion holds for the wrong reason. Replaced with a third transaction
that holds the lots first, so both writers are stopped at the same point and
the collision is arranged rather than hoped for. The assertion changed too,
from "one of them fails" to "the appended record and the running figure still
describe the same lot", which is the property that actually matters.
> **Rule.** A concurrency test that does not force the interleaving is a test
> of the scheduler's mood. Arrange the collision with a lock held by the test
> itself.

**A test got twelve times faster and stayed green.** Letting the generator emit
NUL and ending the case when the domain refused it looked correct and was: the
domain did refuse, the store never saw one. But over 30 characters of rapid's
default rune set NUL is common, so most cases stopped before writing anything
and the entire round-trip suite fell from about a hundred seconds to eight —
still passing, proving a fraction as much. The generator now asserts the
refusal against the domain and then strips the NUL, so every case goes on to do
the round trip it exists for.
> **Rule.** Watch the runtime. A suite that suddenly gets much faster without
> anything being optimised has usually stopped testing something, and it
> announces this by passing. This is the same failure as a check that reads
> nothing: green is not evidence.

One smaller thing: `1.5/3` violates both `basis_terms_are_whole` and
`basis_is_reduced`, and PostgreSQL does not promise which it reports. Asserting
one constraint name by itself made the test depend on evaluation order rather
than on the rule.

#### Verification

Everything below was run, and this lists the commands rather than their
intentions. `make` is still not installed on this machine, so these are the
commands behind the targets, not the targets (the M0 rule).

- `go test -race -coverprofile=coverage.out ./...` — all packages pass.
  `internal/ledger` 91,4% (floor 85), `internal/store` 68,3% (floor 60).
- `./bin/golangci-lint run` — 0 issues. `./bin/golangci-lint fmt` changes
  nothing.
- `grep -rE "float64|float32" internal/ledger/` — no matches.
- `go mod edit -json` — the Go directive is still 1.22. No dependency was added
  this phase.
- `./bin/sqlc generate` — byte-identical output on a second run.
- **Migration 7 down.** Compared through the catalogue rather than a `pg_dump`
  diff, because a dump carries a per-run nonce and comment noise. Every column,
  constraint, index and function after `migrate down 1` from version 7 matches
  a database built by applying migrations 1–6 directly, with one expected
  difference: golang-migrate's own `schema_migrations` primary key, which the
  manual application never creates. `schema_meta.version` reads 6 after the
  down and 7 after the following up.
- Every guard was broken on purpose and watched failing before being restored.
  The coverage list was written before the guards, not derived from them
  afterwards.

#### Deliberately deferred

| Deferred | Lands in |
| --- | --- |
| Authentication, sessions, TOTP, rate limiting with an explicit trusted-proxy list | M2b, next |
| REST API, cursor pagination, structured errors, OpenAPI 3.1 | M2b |
| The round-trip property generator does not produce reversals, so `requireSameTransaction` deliberately makes no assertion about reversal links — an assertion over data that cannot contain the thing is the trap described above. The dedicated store test covers them | M2b or M4 |
| Recording *which* lot-selection policy chose a set of consumptions. Meaningless while FIFO is the only one | M8 |
| A drift check that recomputes every lot's `remaining_amount` from `lot_consumptions` across the whole book. The per-lot reconstruction is tested; the book-wide sweep is not | M6 |
| Why a subtree balance degrades faster than a single-account balance | M6 |
| `fx_rates` | M6 |

### M2b Phase 1 — Authentication

Password hashing, TOTP and backup codes, then the schema and repositories
behind them. `internal/ledger` stays frozen throughout: Phase 0 opened it once
and closed it, and nothing in authentication reopens it.

`internal/auth` is pure. It holds cryptography and policy, defines repository
interfaces, and imports neither `database/sql` nor `net/http` nor
`internal/config` nor `internal/store` — the same arrangement as
`internal/ledger`, enforced by a second depguard list rather than by this
paragraph. The reason is narrower than architectural tidiness: the only
external evidence that the TOTP implementation is correct is RFC 6238's
published vectors, and a crypto test that needs a container is a crypto test
that eventually stops being run.

#### Argon2id: the parameters, and the number they were derived from

`m = 19456 KiB, t = 2, p = 1`, 16-byte salt, 32-byte tag, encoded as a PHC
string so the cost travels with the hash.

The binding constraint is not attacker cost, it is **concurrency × memory on
the weakest host Nusa is meant to run on**. Sixteen simultaneous verifications
at 19 MiB is about 304 MiB, which a 1 GiB machine absorbs. The next commonly
cited figure, 64 MiB, is 1 GiB at that same concurrency and pushes such a host
into swap — where a verification takes seconds and the machine stops serving
anything at all. A password hash that a burst of logins converts into an outage
has traded one security property for another.

**The measurement, and it is the baseline any future change is compared
against:**

| | Median of 5 |
| --- | --- |
| Verification, this development desktop | **28,0 ms** |
| Same, under `-race` | 35,2 ms |
| Derived: Raspberry Pi 4, 5–8× slower at memory-bound work | **150–220 ms** |

That derived figure is the target — the familiar couple of hundred
milliseconds for an interactive login, on the machine that sets the floor — and
it is why iterations are 2 rather than 3. Iterations are also the lever to
reach for if the floor hardware ever moves, because they cost time without
costing memory and so leave the concurrency arithmetic alone.

The first draft justified `t = 2` with the words "already in the right range",
which is not a justification. The number replaced it.

#### Two findings, and the second one is a new shape of mistake

**`Code` panicked on a zero period**, because it derived the counter before
validating and `counterAt` divides by the period. §12 rules out panicking
outside `main`.

What is worth recording is not the bug but how it surfaced. The parameter table
in `TestUnusableAuthenticatorParametersAreRefused` calls **every entry point**
for each bad configuration — `Code`, `CodeAt`, `Validate`, `ProvisioningURI` —
rather than one that seemed representative. Only `Code` had the ordering wrong;
a table testing `CodeAt` alone would have passed, and the panic would have
waited for a misconfigured deployment to find it.
> **Rule.** When a rule is supposed to hold at several entry points, test it at
> all of them. Picking a representative one tests the representative.

**The second finding was not in the code. It was in a claim about a test.**

A comment on RFC 6238's last vector said the row `t = 20000000000` "fails
loudly for any implementation that narrows the counter". Narrowing the counter
to `uint32` left all eighteen vectors green.

20000000000 / 30 is 666.666.666, which is `0x27BC86AA` — and every other T in
Appendix B is smaller. No published vector reaches a counter above 2^32. What
the last row actually proves is that the *clock reading* survives past a 32-bit
`time_t`, which is worth having and is not what the comment claimed.

This is a failure mode the earlier rules do not catch. The guard worked. The
break produced a failure. What was false was the sentence describing *which*
break the guard would catch — and neither a green run nor a red one exposes
that, because the red run looks like success either way.

Fixed from both ends: the comment now states what the row proves, and
`TestTheCounterIsCarriedAtFullWidth` closes the gap with a differential check
over counters that agree in their low 32 bits. That test is labelled in its own
doc comment as self-consistency rather than RFC-backed, because it is.
> **Rules.** Both promoted to §11. Check that what failed is what you expected
> to fail; and never let a guard borrow an external source's authority for
> something that source never said.

#### TOTP

Written over `crypto/hmac` and `encoding/base32`. All eighteen Appendix B
vectors pass, each checked twice — once from the clock through `Code` and once
from the hexadecimal T the RFC publishes through `CodeAt` — so that a failure
says whether the counter derivation or the HOTP construction is wrong.

**The seed trap is asserted, not merely commented.** Appendix B's prose gives
one secret, `12345678901234567890`, while the reference implementation in
Appendix A runs SHA-256 and SHA-512 against 32- and 64-byte seeds of the same
repeating digits. Using the short seed for all three produces three correct
answers and twelve wrong ones, which reads exactly like a broken implementation
and invites someone to "fix" working code.
`TestTheShorterSeedDoesNotProduceTheSHA256Vectors` fails if that ever happens.

**Clock skew is one period either side, and the reasoning is in the code.**
Behind, because people read a code late in its window and type slowly, and
refusing that login is not a security decision — it produces a retry, which is
what the rate limiter counts. Ahead, because a self-hosted server's clock is
less disciplined than a phone's. No wider, because the price is linear: six
digits is a million codes, three windows makes three of them valid at any
instant, and a skew of five makes eleven. A clock more than 45 seconds out is a
broken host and the fix is NTP.

The consequence is stated where it lands: at these settings one code is
acceptable for 60 to 90 seconds, so `Validate` returns the counter it matched
and the caller must record it. Replay prevention is not optional decoration on
top of skew, it is what skew requires.

#### Backup codes: SHA-256, and the arithmetic that makes that safe

Passwords get Argon2id because they are *chosen*, and a chosen secret comes
from a distribution an attacker can enumerate. A backup code is *drawn*, so
there is no distribution and slowness buys nothing — while costing something
real, since checking one code means comparing against every unspent code a user
holds and Argon2id would make one submission ten memory-hard hashes.

That argument only holds if the entropy is past brute force, so it was sized
instead of asserted. At 10^10 SHA-256 per second: 40 bits is two minutes, 50
bits is a day and a half, 64 bits is 58 years, **80 bits is 3,8 million years**.
Ten random bytes, which is exactly sixteen base32 characters — four groups of
four, short enough that somebody will write it down and type it back.

The reasoning lives in `backupcode.go`, at the top, because "why two different
hashes" is the first question anyone reading that file asks.

#### One check was unfalsifiable, and was made falsifiable rather than deleted

Removing the empty-code refusal from `MatchBackupCode` changed no test: an
empty code hashes to something no stored code matches, so the outcome is the
same with or without it. A check no test can distinguish from its own absence
is a check nobody can tell is working.

It was kept and given the one case that reaches it — a stored set containing
`HashBackupCode("")`, which `NewBackupCodes` cannot produce but a corrupted or
imported row could. Deleting the check now turns a test red.

#### Schema: two migrations, and one reserved column that did not fit

Migration 8 adds credentials to `users` by ALTER — the operation migration 2's
own comment promised — plus `sessions`, `user_totp` and `user_backup_codes`.

Migration 9 reshapes something M2a left behind. `idempotency_keys.response
jsonb` was commented "reserved for M2b, which replays the original HTTP
response body", and checked against what a replay actually has to reproduce, a
body is not enough: **the status line is part of the response and is not
derivable from the body.** A create that answered 201 replaying as 200 has not
replayed, and neither has a 422 validation failure coming back 200 with the
error document presented as a result. It is now `response_status smallint` and
`response_body jsonb`, with a constraint refusing half of one.

Nothing writes the column yet, so this was the last moment it was free. Doing
it after the first deployment would have meant a backfill with a guessed
status. Migration 6 was left exactly as written — migrations are forward-only,
and editing an applied one is how two databases claiming the same version come
to differ.

No column was added for response headers. The only one a replay must reproduce
is `Location` on a 201, and `entity_kind` and `entity_id` are already stored;
the route for a kind is a fact about the API, not about the request. A stored
string would be a second copy of something derivable, free to drift the first
time a route changes.

#### Registration: the rule is an index, not a check

The first person to reach a fresh instance may register; afterwards
registration is closed until M9 brings invitations.

A `SELECT` then `INSERT` cannot promise that. Under READ COMMITTED two
simultaneous registrations both see an empty table and both proceed. Rather
than add a lock every future caller must remember to take, the rule is
expressed as something the database cannot violate:

```sql
CREATE UNIQUE INDEX users_at_most_one_credentialed
    ON users ((email IS NOT NULL)) WHERE email IS NOT NULL;
```

Every credentialed row indexes the same key, so the second insert collides with
the first. The collision is also **deterministic rather than lucky**: a second
inserter blocks on the uncommitted index entry until the first transaction
resolves, then fails. That is what makes it testable without racing goroutines
and hoping.

The test runs the gate both ways, because the two arrangements fail
differently. With the gate committing, both contenders lose to a row they can
see. With it rolling back, the entry they were waiting on vanishes and exactly
one wins — and that second case is the one that proves the index rather than
merely proving that something blocked.

`HasCredentialedUser` exists and is documented as unable to gate anything: it
answers what a registration form should offer, and its answer is stale the
instant it is read.

A user row with no credentials is not a half-finished account. It is a
non-human actor — the rule engine, an importer — which M2a already required,
since `idempotency_keys.actor_id` is NOT NULL and a replayed rule write must be
scoped to somebody. Those rows neither count as registrations nor close the
door on one, and a constraint keeps `email` and `password_hash` travelling
together so their joint absence stays a meaningful state.

#### Sessions

The cookie carries a 256-bit random token; the table stores only its SHA-256.
A leaked database therefore yields no usable cookie. It is also why a session's
identity and its credential are two different values: `id` appears in logs and
in the audit trail, while the token exists only in the response that set it.

**Revocation is a column, never a delete.** A deleted row cannot tell "revoked"
from "never existed", and the first of those is something the audit trail and a
person's own session list are entitled to show.

**Three things end a session and they are resolved in one place** — expiry,
revocation, and a password changed since the session began. The last is a
backstop that needs no rows rewritten: a caller that changes a password and
forgets to revoke has still ended the old sign-ins. A caller obliged to
remember three conditions eventually remembers two.

**Awaiting a second factor is a state, not a refusal.** That session must be
returned, because it is the credential the second step itself presents.
Refusing it would make completing two-factor authentication impossible — and
the first draft of the SQL comment said it was one of the "ways to be
unusable", which would have been a specification for exactly that bug.

**Elevation rotates the token.** A session that keeps its cookie across the
step that raises its privilege is a session fixation: whatever learned the
pre-authentication token holds a fully authenticated one the moment the person
finishes. The statement also refuses to elevate twice, so a replayed completion
cannot mint a second live token for one row.

#### Two similar races, two different mechanisms — and only one of them earned

TOTP and backup codes both have a "two submissions of one secret" problem, and
the obvious move is to solve both the same way. Breaking each solution showed
they are not the same problem.

**TOTP needs the row lock.** Its write is an unconditional
`SET last_used_counter = $2`, so nothing in the statement can tell that another
transaction already advanced it. Removing `FOR UPDATE` turns the concurrency
test red: both readers see the old counter, both find the code unspent, both
succeed.

**Backup codes do not.** Spending one is naturally conditional — the row must
still be unspent — and `MarkBackupCodeUsed` says so in its own WHERE. Under
READ COMMITTED the second UPDATE blocks on the first one's row lock,
re-evaluates against the committed version and matches nothing. The lock that
was originally written there was removed after **removing it left the
concurrency test green**, which is the only way that would ever have been
noticed.

Both findings are recorded in the SQL beside the statements, including how each
was established, so the absent lock is not helpfully restored by someone later.
The comment on `ListUnusedBackupCodeHashes` says in as many words that a lock
there would add no guarantee and asks the reader not to add one back believing
it protects something — an absent mechanism needs a louder note than a present
one, because its absence looks like an oversight.
> **Rule.** A construction copied for uniformity with somewhere else is not
> known to do anything in its new home. Consistency is a reason to *look* at a
> mechanism, never evidence that it is load-bearing where it now sits. The only
> way to find out is to take it out and see whether anything goes red — and if
> nothing does, the honest outcomes are to remove it or to label it as not
> being the guarantee. Leaving it in place unlabelled teaches the next reader
> that it is doing the work.

A third break sharpened it further. Removing the `used_at IS NULL` clause from
the UPDATE fails the *concurrent* test but not the sequential one, because the
list query filters spent codes and a replay presented later never reaches the
matcher. So the list filter covers the sequential case, the UPDATE clause
covers the concurrent one, and only the UPDATE clause is uniquely necessary.
The filter stays as the correct question and the right index, labelled as not
being the guarantee.

#### What went wrong, and what it teaches

**A CHECK constraint caught a statement-ordering bug.** `ConfirmTOTPEnrolment`
wrote the counter before marking the enrolment confirmed, and
`user_totp_unconfirmed_has_no_counter` refused it. The two statements are in
one transaction and the end state is legal, but a CHECK is evaluated per
statement rather than at commit. Order reversed.

**The break harness was contaminating its own results.** It regenerated sqlc
when a break edited a `.sql` file, but restoring that file afterwards does not
restore the generated `.go`. Every break following a SQL break therefore ran
against the previous break's generated code, and three of them appeared to fail
tests they had nothing to do with — including one failure that looked exactly
like a flaky test and could not be reproduced in six attempts.

This is the §11 rule about checking *what* failed, arriving inside the tool
built to apply that rule. Nothing was wrong with the code or the tests; the
apparatus was lying, and the only thing that exposed it was a predicted failure
list that the results did not match.
> **Rule.** A harness that mutates generated artefacts must restore them as
> deliberately as it restores their sources. Restoring an input is not
> restoring an output.

**Five predictions were wrong and the code was right.** Each was recorded and
corrected rather than quietly widened after the fact: a break to token hashing
fails every test that creates a session, not one; ignoring revocation fails two
tests, not one; refusing pending sessions did not fail the elevation test until
that test was changed to look the session up by its cookie, which is what the
real flow does.

That last one was a genuine coverage gap the prediction found. The elevation
test had been passing a session id straight in, so it never exercised the
lookup that a handler must perform first — and refusing pending sessions would
have broken two-factor authentication in production while the test stayed
green.

#### Verification

`make` is still not installed here, so these are the commands behind the
targets rather than the targets.

- `go test -race -coverprofile=coverage.out ./...` — every package passes.
  `internal/auth` 91,0%, `internal/store` 70,4% (was 68,3%), `internal/ledger`
  91,4% and untouched.
- `./bin/golangci-lint run` — 0 issues. `./bin/golangci-lint fmt` changes
  nothing.
- `./bin/sqlc generate` — byte-identical on a second run.
- `git status --porcelain internal/ledger` — empty. The freeze holds.
- `go mod edit -json` — Go directive still 1.22, no dependency added.
- **Migrations 8 and 9 down, verified through the catalogue** rather than a
  `pg_dump` diff. Columns, constraints, indexes, tables, functions, triggers
  and comments after `migrate down` match a database built by applying the
  earlier migrations directly, with the one expected difference golang-migrate
  contributes: its own `schema_migrations` table.
- The catalogue comparison was itself broken on purpose three times, with the
  outcome predicted first: removing a `DROP TABLE` leaves 29 objects behind;
  removing a `DROP COLUMN` leaves 2; removing a `DROP INDEX` leaves **none**,
  because dropping the column cascades to it. That third result is why the
  down migration now says those two `DROP INDEX` lines are redundant and kept
  deliberately.
- 18 deliberate breaks of the store guards, each watched failing **and** checked
  against the test it was predicted to fail.

One correction to an earlier report: the first pass of the catalogue check
produced an empty diff because the SQL file never reached the container and
both sides were empty. A check that reads nothing passes. It was rerun with a
line count asserted first.

#### Configuration, client addresses and the login limiter

Three pieces, none of them wired to a handler yet: the configuration that
describes them, the function that decides whose address a request belongs to,
and the limiter that counts failed sign-ins.

`internal/config` now imports `internal/auth`, and the direction is safe
because depguard already refuses `internal/config` inside `internal/auth` — the
cycle cannot form. The reason to import at all is the Argon2id floor: the floor
has to be *the same number* as the default, and expressing it as
`auth.DefaultParams()` rather than a copy means there is one definition instead
of two that drift.

#### The Argon2id cost may be raised and never lowered

`auth.DefaultParams()` is the floor as well as the default, so every cost
variable is one-directional. Configuration exists to make the cost *stronger*.

A number with a knob on it that can be turned down is a number that eventually
gets turned down — usually by somebody trying to make a slow test suite or a
small container behave — and the result is a password store weaker than the
project believes it is, with nothing anywhere saying so. There is no legitimate
deployment that needs less, either, because DefaultParams was already derived
from the weakest host Nusa targets.

The ceiling is `auth.MaxVerifiableMemory` rather than a number chosen in
config, and that is not tidiness. Minting above it would produce hashes
`auth.Verify` then refuses to read, so the credential table would fill with
rows nothing can check. The two constants have to agree, so there is only one.

#### Rate limiting: the key is the address, and never the account

The tension is real and the choice is recorded rather than made quietly.

Counting failures per account stops one account being brute-forced, and hands
anyone who can reach the login form a way to lock a named person out: wrong
passwords for their address until the limit trips, repeated forever. For Nusa
that is not a trade between comparable harms. Nusa is self-hosted and until M9
holds exactly one account, so a per-account limiter *is* a per-instance
limiter, and any stranger who can reach the page can deny the only user access
to their own finances.

So the key is the client address. What it costs:

- **An attacker with many source addresses is not slowed.** Acceptable because
  the limiter was never the main defence against guessing — Argon2id is. At the
  measured cost a server answers a few attempts a second with no limiter at
  all, which is nothing against a password with real entropy. The limiter stops
  one source from making that rate matter, and stops a scripted flood from
  spending a small machine's memory on Argon2id.
- **People behind one NAT gateway share an allowance.** Bounded rather than
  open-ended: a key at its limit records no further failures, so a flood cannot
  hold an address blocked indefinitely. The allowance refills every window
  however long the attempt runs — measured at exactly `limit` openings per
  window under continuous failure — so a co-located user keeps getting chances.

Failures are counted, not requests. A limiter in middleware throttles a browser
reloading a login page and does nothing about credential stuffing sent as
well-formed POSTs. A successful sign-in clears the count, so somebody who
mistypes four times and then succeeds is not left one attempt from being locked
out of their own instance.

**This decision is conditional, and the condition is written down because it
will expire.**

The argument above is not a general claim about rate limiting. It rests on one
fact about Nusa as it stands: an instance holds exactly one account, so a
per-account limiter *is* a per-instance limiter and locking the account is
locking the service. Take that fact away and the arithmetic changes — with
several accounts, a per-account counter denies one person and leaves the others
working, which is a far smaller harm than it is today and may well be worth the
protection it buys.

| | |
| --- | --- |
| **What makes the decision correct** | an instance has at most one credentialed account, enforced by `users_at_most_one_credentialed` |
| **What invalidates it** | the instance can hold more than one account |
| **What brings that about** | **M9**, which adds households and invitations and drops that index |

So M9 must reopen this, not inherit it. The shape to weigh then is a
per-account counter that *slows* rather than blocks — never one that locks —
layered on top of the per-address limiter rather than replacing it.

A decision that is right today because of one fact goes silently wrong when the
fact changes, unless somebody wrote down which fact it was.

#### Client addresses: what the tests had to prove

`api.ClientIP` walks X-Forwarded-For from the right, strips hops that are
themselves trusted, and stops at the first that is not. X-Real-IP,
True-Client-IP and Forwarded are never read at all — a single value carries no
chain, so nothing about it can be verified, and a header that cannot be
verified is not a weaker signal but no signal.

The four properties, each broken on purpose:

| Property | How it was falsified |
| --- | --- |
| A forged header from an untrusted client changes nothing | skip the peer-trust check: the forgery wins |
| An empty list ignores the header entirely | see the note below |
| Stripping stops at the first untrusted hop | walk left to right: the client's planted address wins |
| A long chain is bounded | remove the hop cap: an address planted past 200 hops decides the answer |

The long-chain guard asserts a **value** rather than a duration. The header
puts an untrusted address far to the left of 200 trusted hops, so an
implementation that walks the whole chain finds it and one that stops at the
cap never does — which distinguishes them without a stopwatch, and a timing
assertion would have been the flakiest thing in the file.

**One branch is not independently falsifiable and is labelled as such.** The
explicit `len(trusted) == 0` check is subsumed by the check after it, because
an empty list contains no peer either way. It is kept because it states the
rule rather than an optimisation: if `inAny` were ever changed to treat an
empty list as matching everything, this line is what stops that becoming
blanket trust. Recorded here so nobody later removes it believing it does
nothing, and so nobody believes a test covers it.

#### Two guards that proved less than they claimed

**A multi-line header test could not tell the two orders apart.** Reversing the
loop over header lines left it green: its untrusted address was leftmost under
either reading, so both orders produced the same answer. The fix is a case
where the order decides — a forgery on the first line, the truth appended to
the last — after which reading forwards returns the forgery. Two axes, covered
separately: one break for order *within* a line, one for order *between* lines.

**A limiter test asserted something incoherent.** It flooded failures forward
to t=103 and then queried at t=59, so the limiter was being asked about the
past after being told about the future, and it answered `1m1s` because
`now.Sub(oldest)` was negative. The code was right; the test had time running
backwards. Rewritten to flood only inside the block, which is the property
actually wanted: hammering during a block does not push its own release out.

#### Decision (c), and why it has no variable

`Config.SecureCookies()` is derived from `Env` and reads nothing. In production
it is true and no environment variable changes it.

The falsification is the change somebody would actually make: add a field, read
`NUSA_COOKIE_SECURE` in `Load`, consult it in `SecureCookies`. The test sets
fourteen plausible spellings — `NUSA_COOKIE_SECURE`, `NUSA_INSECURE_COOKIES`,
`NUSA_TLS`, `NUSA_DEV` and the rest — to six values meaning "off", and asserts
the answer is still true. With the field added it goes red.

The structural point is stronger than the test: `Config` does not retain the
lookup function, so `SecureCookies` *cannot* read a variable without somebody
adding one first. `NUSA_ENV`'s own comment claimed it "only affects operational
defaults such as log formatting", which stopped being true the moment this
landed; both that comment and the field's doc were corrected.

#### The CIDR validator refuses rather than guesses

Every rejection is a case with two readings where the parser would silently
pick one:

- **Host bits set.** `netip.ParsePrefix` accepts `10.0.0.1/24` and masks it to
  `10.0.0.0/24` — 256 addresses when the operator may have meant one. Refused,
  with both readings named in the message.
- **`0.0.0.0/0` and `::/0`.** Trusting every client to state its own address is
  the blanket trust §10 exists to forbid, and worse than not configuring
  proxies at all because it looks deliberate.
- **Empty entries.** A trailing or doubled comma means the value was assembled
  by something that got it wrong; skipping the gap hides that.
- **Exact duplicates.** Overlapping ranges are legitimate and accepted; the
  same entry twice is a copy-paste error.
- **An IPv4 range in IPv6 form.** `::ffff:10.0.0.0/104` and `10.0.0.0/8` are
  the same network with prefix lengths 96 apart, and converting between them by
  arithmetic is the quiet reinterpretation this validator exists to avoid. A
  *bare* `::ffff:10.0.0.1` is accepted and normalised, because a single address
  has no length to get wrong.

A bare address is accepted as a single host, because one address unambiguously
means one address and requiring `/32` everywhere would be ceremony for the
commonest case: one reverse proxy, one address.

#### Verification

`make` is still not installed here, so these are the commands behind the
targets.

- `go test -race -coverprofile=coverage.out ./...` — every package passes.
  `internal/api` 84,2% (was 70,2%) — measured at this point in the phase, before
  the endpoints and middleware below were written, so it is not comparable with
  any figure reported after them and is not a baseline for one; `internal/auth` 92,6%, `internal/config`
  92,0%, `internal/ledger` 91,4% and untouched, `internal/store` 70,4% and
  untouched.
- `./bin/golangci-lint run` — 0 issues. `noctx` is now excluded for `_test.go`
  alongside `gosec`, with the reason in the configuration: it exists to catch
  outbound requests made without a deadline, and `httptest.NewRequest`
  constructs a request rather than making one.
- `go mod edit -json` — Go directive still 1.22. No dependency added; all of
  this is `net/netip`, `sync` and `strconv`.
- 25 deliberate breaks, each watched failing and each checked against the tests
  it was predicted to fail. Six predictions were wrong on the first pass; one
  of those was a genuine coverage gap and is described above, and the other
  five were breaks that legitimately fail more tests than expected.

#### Environment

Docker Desktop stopped by itself again — the fourth time across M2a and M2b.
The daemon answered about ten seconds after being relaunched, which matches the
M2a note: wait on `docker info` succeeding rather than on the named pipe
existing.

#### Endpoints, middleware, and the three debts that came due here

Registration, sign-in, the second-factor step, sign-out and one authenticated
route, plus the middleware they hang off. This is where the pieces built in
isolation meet: `ClientIP` decides the limiter's key, `VerifyDecoy` sits on the
no-such-account path, and `Config.SecureCookies` decides one cookie attribute.

**Nothing is cached, and that is the property only this layer can establish.**
The store proves a revoked session stops resolving; what it cannot show is
whether the middleware ever asks again. A cache holding sessions for even a few
seconds would leave a revoked token working for that long and every store test
would still pass. So the middleware resolves per request, and a test counts the
lookups — five requests must produce five.

**What is not achieved, stated rather than implied.** A handler that has
already passed the middleware is not interrupted. The test arranges a genuine
overlap — the handler blocks until the test releases it, so the revocation
provably commits while the first request is still inside — and proves that
concurrent and subsequent requests are refused from that instant. The in-flight
one completes. Nothing short of cancelling its context mid-flight would change
that, and every Nusa endpoint is a short read or write, so the window is
milliseconds.
> **Debt.** A watcher that cancels the request context on revocation earns its
> per-request goroutine the day there is a long-lived endpoint — a server-sent
> event stream, a long poll — and not before.

#### Two right requirements that cancel each other

The instruction was that failed sign-ins reach `audit_log`, because a log that
records only successes is useless for an investigation. That is correct on its
own. Following it would have undone the defence built three steps earlier.

An anonymous attempt has no actor, and `audit_log_human_origin_has_an_actor`
refuses a human origin without one — so a row could be written for a wrong
password against a real account and *not* for an address that does not exist.
That asymmetry is a database write on one path and not the other, which is a
timing difference, which is precisely the account-enumeration oracle that
`VerifyDecoy` and the single shared refusal message exist to close.

So failures go to the structured log, identically on both paths, carrying the
`account_exists` flag an operator needs and an attacker cannot see.
`audit_log` takes what has an actor: registration, sign-in, second factor,
sign-out.
> **Rule.** Requirements that are each sound can still be unsatisfiable
> together through one path, and the conflict is invisible from either one
> alone. What surfaced this was not asking whether the requirement was met — it
> was asking what the code *does on both branches* and noticing that one of
> them now touches the database and the other does not. Check the shape of the
> work on every path, not the presence of the feature on the path you were
> thinking about.

> **Debt, named with its trigger.** A separate `auth_events` table is the right
> home for anonymous attempts, and is deferred rather than forced. The moment
> an operator needs a queryable history of attempts that have no actor, that is
> **a migration of its own** — a new table with its own shape — and never a
> relaxation of the human-origin constraint, which is what makes the rest of
> the audit log worth reading. (This said "migration 10" when it was written;
> Phase 2 took that number for something else. Pinning a number to work that
> has no trigger date is how a note goes stale — the shape is the promise, not
> the number.)

#### A test that arranged its own answer

The check that a successful sign-in clears the rate-limit count advanced the
clock two minutes past a one-minute window before signing in. The count had
therefore expired on its own, and the following attempts were allowed whether
or not anything had cleared anything. Removing the `Succeed` call left it
green.

It is a shape worth naming because it does not look like a broken test. Waiting
out a timeout, seeding the expected value, asserting a default — all of them
read as ordinary arrangement, and all of them make the subject a passenger.
Rewritten without advancing the clock: four failures out of an allowance of
five, a success, then four more failures that must all be refused as wrong
passwords rather than as rate limiting.
> **Rule.** Promoted to §11. Ask what would happen if the function under test
> were deleted outright, not merely changed.

A second test had the same defect in a different costume. "A pending session
may sign out" asserted only that a later request returned 401 — but a wrong
one-time code returns 401 too, so it passed without sign-out revoking
anything. It now asserts the error *code*: `unauthenticated`, not
`invalid_credentials`.

#### What the responses are allowed to say

One code and one status for every credential failure — no such account, wrong
password, wrong one-time code, spent backup code — and one for every refused
registration, whether the instance is full or the address is taken. The tests
compare **whole response bodies** rather than codes, because a difference
anywhere in the body is the oracle.

`writeInvalidCredentials` exists as a single function rather than four call
sites for the same reason: a refactor that touches one branch is how the two
answers drift apart, and the drift is the leak.

#### Smaller decisions worth knowing

**`PasswordHasher` is an interface for exactly one reason.** `VerifyDecoy` has
no return value and no observable effect, so proving the no-such-account path
calls it needs either a counter or a stopwatch — and a timing assertion in CI
is a flake waiting to happen. A wrong password deliberately does *not* spend a
decoy: it already spent a real verification, and counting both would make the
failure path cost twice the success path, which is the same leak pointing the
other way.

**The cookie name carries `__Host-` in production.** That prefix is a promise
the browser enforces: a cookie so named is refused unless it is Secure, has
Path=/, and has no Domain. What it buys is that a subdomain cannot set a
session cookie for the parent — on a self-hosted box a subdomain is exactly
what an attacker is most likely to control. Development cannot use it, because
the prefix requires Secure and Secure is off there, so the name differs between
environments and switching `NUSA_ENV` invalidates existing cookies. That is the
correct outcome: a session minted under one set of transport guarantees should
not silently carry over into another.

**gosec's G124 is suppressed rather than obeyed, twice.** It wants `Secure` set
to a literal `true`. Obeying it would mean nobody could sign in over
`http://localhost`, and it would replace the derivation decision (c) exists to
protect with a constant. `HttpOnly` and `SameSite` *are* literals, which is the
part of the rule that should be unconditional.

**`NewUUIDv7` is fifteen lines here rather than a direct dependency.**
`google/uuid` is already in the module graph indirectly; promoting it would put
it in the application's own graph for a function that is a timestamp, two
nibbles and some randomness. §12 asks for a justification for a new dependency
and there is not one.

**Registering does not sign anybody in.** The password was chosen rather than
presented, and a flow that hands out a session on registration is one where the
credential is never actually tested before it grants access.

#### State after M2b Phase 1

The ledger is reachable by a person. Somebody can register on a fresh instance,
sign in, be asked for a second factor if they have one, complete it with a
one-time code or a backup code, hold a session that is resolved from the
database on every request, and sign out in a way that ends the session rather
than forgetting it locally.

`internal/ledger` is byte-for-byte unchanged across the whole of M2b Phase 1.
The freeze that Phase 0 closed has held: authentication added a package, two
migrations, a repository and an HTTP surface, and touched the domain not at
all.

What does **not** exist: any endpoint that reaches the ledger. `/api/v1/auth`
is the whole of the REST surface. Accounts, transactions and commodities,
cursor pagination, idempotency through HTTP, and the OpenAPI document are the
next phase, and they arrive behind a session middleware that already works.

**The debts that leave this phase, each with the condition that calls it in:**

| Debt | What brings it due |
| --- | --- |
| Cancelling an in-flight request on revocation | the first long-lived endpoint — SSE, a long poll, anything that outlives a few milliseconds |
| An `auth_events` table for anonymous attempts | an operator needing a queryable history of attempts that have no actor. A migration of its own, never a weaker constraint |
| Re-weighing per-account rate limiting | **M9**. The current decision is correct only because an instance holds one account, and M9 removes that |
| Encrypting the TOTP secret at rest | a key that genuinely lives somewhere other than beside the database backup |
| Sweeping expired sessions on a schedule | `SweepExpiredSessions` exists and nothing calls it periodically |
| TOTP enrolment and backup-code endpoints | a person can be *asked* for a second factor but cannot yet set one up over HTTP; the store and the domain are complete |

#### Deliberately deferred

The debts carried out of this phase are in the table above, each with the
condition that calls it in rather than a milestone number, because most of them
are triggered by a change in the product rather than by a date.

What is deferred to a *named* milestone:

| Deferred | Lands in |
| --- | --- |
| The REST API over the ledger: `/api/v1/accounts`, `/transactions`, `/commodities`, cursor pagination, idempotency through HTTP, OpenAPI 3.1 generated from the code and validated in CI | M2b Phase 2 |
| TOTP enrolment, confirmation and backup-code endpoints. The store and the domain are complete and tested; only the HTTP surface is missing, so a second factor can be demanded but not yet configured | M2b Phase 2 |
| Encrypting the TOTP secret at rest under a key kept away from the database. Storing it in clear is recorded as a decision, not an oversight — a key sitting in the same `.env` as the same backup protects nothing | Unscheduled |
| Sweeping expired sessions on a schedule. `SweepExpiredSessions` exists; nothing calls it periodically | M12 |

### M2b Phase 2 — decisions taken before any code

Recorded ahead of the implementation, following the precedent set by *M2 split
into M2a and M2b*: the reasoning is about what the HTTP surface is allowed to
be, and it is worth having on record independently of how the work turns out.
The implementation record is appended when the phase completes.

#### "Full CRUD" does not apply to the ledger, and the prompt was wrong

`.dev/PROMPT-MILESTONE.md` asked for *CRUD penuh* over `/transactions`. §5.3
makes the journal append-only. Those cannot both hold, and §5.3 wins: a PUT
that edits a posting is precisely the mutation the immutability of every domain
type exists to make impossible, and an HTTP layer offering one would be asking
the store for something no store method exposes.

So the verbs are chosen by the accounting model rather than by REST habit:

| Verb | What it does |
| --- | --- |
| `POST /transactions` | creates |
| `PUT` / `PATCH` | **does not exist** on a transaction or a posting, at any path |
| `POST /transactions/{id}/deletions` | writes a reversal with `kind=deletion` |
| `POST /transactions/{id}/corrections` | writes a reversal with `kind=correction` |
| `DELETE` | **does not exist** — see below |

The reversal's date is required in the body and is never defaulted, because
`ledger.Reverse` refuses to guess it and refuses for a reason: booking a
reversal today leaves last year's report intact, booking it on the original's
date rewrites that period, and choosing between those is an accounting decision
(§5.4).

**`DELETE /transactions/{id}` was proposed first and withdrawn**, and the
reasoning generalises to any verb. A reversal needs an identity, a date and a
map of posting identities, so this DELETE could never have been bodiless; the
familiar shape of the verb was not on offer whatever we chose. What settled it
is that a body on DELETE is *undefined* rather than merely unusual — HTTP
clients, proxies and libraries are entitled to drop it, and several do. An
endpoint resting on that fails by environment rather than by logic: it works in
the test suite, works from curl, and fails behind one particular proxy with a
request that arrives looking like a client that simply forgot its body. That is
the most expensive failure shape available, because nothing in the error points
at the cause.

Symmetry is the second reason and the smaller one. Corrections and deletions
are one mechanism — `Reverse` with a `Kind` — and two differently shaped doors
into it is how the two drift apart, which is exactly why `Reverse` is one
builder rather than two.

Accounts are not covered by this. §5.3 is about the journal — postings and the
transactions holding them — and an account is a mutable label sitting beside it,
so `PATCH /accounts/{id}` is legitimate and exists. The first draft of this
entry said PUT and PATCH exist nowhere "at any path", which would have
contradicted the account decision two paragraphs below it.

The prompt file was corrected in place rather than left to mislead the next
session — the same reasoning that put `LICENSING.md` in the repository: a
normative document only one person has read is not normative.

**Accounts are the narrower case.** `name` and `closed` may be updated;
`kind`, `parent` and `commodity` never. Postings already written depend on all
three — an account's kind decides how every balance built from it reads — so
changing one would invalidate answers already given. The restriction is
enforced in `internal/store`, not in the handler: a handler that forgets to
validate must not be able to change a kind, and the second HTTP caller of that
method is the one who would find out.

#### Disposals are supported, and only the response shape is provisional

The store's `SaveDisposal` is complete and tested and has no caller outside
tests. A write path that is finished but unreachable is a write path that rots,
so `POST /transactions` accepts an optional list of posting identities naming
the lines that reduce a holding, and routes to `SaveDisposal`.

The two halves have different futures, so only one of them carries the label.

The **request** field is not a design choice at all. `SaveDisposal` derives the
account and the quantity from the line itself, deliberately, so the quantity
has one home and cannot disagree with the posting. What it cannot derive is
which lines are disposals rather than ordinary reductions — inferring that from
"the account happens to hold lots" is exactly the inference the store declines
to make. The field is therefore the minimum restatement of the one fact the
store must be told, and M7 cannot make it smaller. A higher-level endpoint
(`POST /holdings/{id}/sales`, building the transaction server-side) is additive
if M7 wants one; this stays as the low-level door.

The **response** is the provisional half. Reporting which lots a sale drew on
means encoding `ledger.Consumption`, whose `Basis` is a `ledger.Rat` — and
`Rat` has no `MarshalJSON`, unlike `Money`, `Date` and `Rate`. That absence is
not an oversight to fill in passing: §4.7 makes `Rat` the exact intermediate,
and deciding how an unrounded basis crosses the wire is deciding whether a
client may do arithmetic on it. That is an investment-API question with no UI
to test it against yet.

So Phase 2 returns the disposal's transaction and nothing about the lots it
consumed. `Consumptions` and `ConsumedFromLot` stay reachable only from Go.

> **Trigger.** **M7** revisits this, and the change it may make is additive: a
> consumption detail in the response, or a separate resource for it, decided
> against a real holdings UI. `Rat`'s wire encoding is decided there, once,
> rather than guessed here.

#### Any authenticated session may read and write the whole book

There is no `user_id` on `accounts` or `transactions`, and none is added here.
Every ledger endpoint is authorised by holding a session, and by nothing
narrower.

| | |
| --- | --- |
| **What makes the decision correct** | an instance has at most one credentialed account, enforced by `users_at_most_one_credentialed` |
| **What invalidates it** | the instance can hold more than one account |
| **What brings that about** | **M9**, which adds households and invitations and drops that index |

This is the **second** security decision resting on that one index. The first
is the choice to key rate limiting on the client address rather than on the
account, recorded under M2b Phase 1.

**When M9 drops the index, two decisions fall at once, not one** — and they
fall in different files, neither of which mentions the other. M9 must reopen
both together: a per-account rate limiter that slows rather than blocks, *and*
row ownership on every ledger table. Finding one and not the other leaves an
instance that either locks a named person out of their own finances, or lets
every member of a household read every other member's book.

A decision that is right today because of one fact goes silently wrong when the
fact changes. A fact holding up two decisions goes wrong twice, and the second
one is the one nobody is looking for.

#### Smaller decisions, settled before the code

**Idempotency-Key is required on every mutation**, and its absence is a 400
with a stable code rather than a key minted server-side. Minting one would
produce endpoints that look idempotent while every retry writes again, which is
worse than an endpoint that is honestly not idempotent, because nothing in the
response says which of the two you are holding. `store.Write` requires the key
already; this is the edge agreeing with it rather than working around it.

**The cursor is keyset, opaque, and carries a hash of the filter set.**
Changing a filter mid-pagination is refused rather than reinterpreted — the
alternative is a page that silently answers a different question than the page
before it.

It is **not signed**, and the reason belongs where the cursor is decoded rather
than only here: everything a cursor names is already readable by the session
presenting it, so forging a position grants nothing that asking politely would
not. That is a fact about *this* resource under the authorisation decision
above, not a general claim that cursors need no integrity. A cursor that
encoded a filter the server would otherwise impose would need signing, and the
next person to add one must not read this as precedent.

**OpenAPI 3.1 is hand-written and verified from both directions in CI.** The
prompt asked for a spec generated from the code; in Go that means comment
annotations, which drift from the handler beside them and report nothing when
they do. The real requirement is *valid and matching the implementation*, and a
two-way check enforces it better than a generator: every mounted chi route must
appear in the document, **and** every path in the document must exist in the
router. The second direction is the one usually left out, and it is the one
that catches a route deleted from the code and left standing in the spec. Both
are to be watched failing (§11).

#### When an error code is allowed to exist

Every `ErrorCode` is an i18n key: §9 forbids hardcoded user-facing strings, so
the client renders from the code and never from `message`. That makes the list
of codes a public vocabulary, and a vocabulary grows without anyone deciding to
grow it. Left alone it becomes a second copy of the domain's error taxonomy,
one code per way of being wrong, and it gets there in single steps that each
look reasonable.

> **Rule.** A new error code is justified only when a client acts differently
> because of it, or when the person reading the screen must be told something
> different. Everything else is `validation_failed` carrying `field` and
> `details`. This binds every milestone, not only this one: the question to
> answer before adding a code is what the caller would do differently on
> receiving it, and "it would show a more specific message" only counts when
> somebody can say what that message is.

`message` is developer-facing English and is never displayed. `details` carries
machine-readable specifics — amounts as strings, per §4.5 — and never
pre-formatted prose, because prose in `details` is a user-facing string that
went around the catalogue.

#### In progress — what the HTTP idempotency layer found

The phase's implementation record is written at the end. This one finding is
here early because it changed the schema, and a migration whose reason lives
only in a commit message is a migration the next session has to reverse
engineer.

**`response_body` was `jsonb`, and jsonb is not a byte store.** The column
exists to be handed back exactly as the first attempt sent it, and that is the
one thing jsonb does not do. Measured against PostgreSQL 16, storing
`{"z": 1,  "a"  :  "two", "a": "three"}` reads back as `{"a": "three", "z": 1}`
— keys reordered, insignificant whitespace dropped, the duplicate key
discarded. All three are documented behaviour and all three break the only
promise the column makes.

It matters little for a client that parses JSON and a great deal for one that
does not: a caller comparing a replay against what it received the first time,
hashing it for a cache, or checking it against a signature is looking at two
different answers to one request. **Migration 10** changes the type to `json`,
which stores the text as given and still refuses a document that is not JSON.
Nothing indexes this column, nothing queries into it, and no JSON operator is
ever applied to it, so the decomposition jsonb performs was paid for and never
used.

What is worth keeping is how it surfaced. The HTTP layer's comment already
claimed a replay repeats the original bytes, and every guard above it passed —
because they ran against a fake that stores what it is given. The claim was
only ever as true as the column, and nothing in the API package could tell.
The store test that found it was written to assert the thing the layer above
was assuming, with a deliberately awkward document that a well-behaved one
would have hidden.

> **Rule.** When a layer's comment makes a promise that a lower layer actually
> keeps, the guard belongs at the lower layer, and it needs input chosen to
> break the promise rather than input that looks realistic. A fake cannot
> falsify a claim about storage — it will faithfully return whatever it was
> handed, which is precisely what the real thing does not do.

Two smaller findings, both about tests rather than code. A test asserted
`errors.Is(err, ErrInvalidWrite) || ledger.ValidateID("not-a-uuid") != nil`,
whose second half is true on every run, so the assertion could not fail
whatever the code did — the §11 shape of a test arranging its own answer,
found by reading rather than by running. And the new store tests were the only
ones in that package marked `t.Parallel()`, which is not a speed-up there:
`open(t)` truncates the shared container, so they were deleting one another's
fixtures. Eight tests failed for that reason and exactly one of them had a
real defect behind it.

#### Measuring one change when the suite is noisy

The store suite went from about 80 seconds to about 120 with keyset pagination
added, and §11 says a large change in how long a suite takes is a signal in
either direction. Chasing it produced a technique worth keeping.

The new tests accounted for 6,6 seconds, so they were not it. The real suspect
was `LoadTransaction`, which now reads its postings through
`ListPostingsByTransactions` with a one-element array rather than through a
singular query with `= $1` — and it is called thousands of times by the
round-trip property test.

**The predicate was measured on its own, with the Go signature held fixed.**
The SQL changed from `transaction_id = ANY($1::uuid[])` to
`transaction_id = ($1::uuid[])[1]`: still an array parameter, so sqlc generates
exactly the same function, and nothing above the query moves. `ANY` against
equality came out at 10,87s versus 10,87s, with 11,30s on a repeat of the
first — no measurable difference.

That fixed signature is the whole point. Changing the shape of a function while
timing it measures two things at once and attributes both to whichever one you
were thinking about.

**The baseline is noisy, and here are the numbers so the next session does not
have to rediscover them.** `TestBalanceLatencyStaysWithinItsBudget` loads
500.000 postings and takes about 98 seconds by itself; the whole store package
measured 92,9 and 96,8 seconds on two consecutive runs without `-race`, and
118–121 with it. A suite that appears to have moved by thirty seconds has told
you nothing yet.

#### Environment: backslashes do not survive the heredoc path here

Writing Go or SQL through a shell heredoc in this environment loses one level
of backslash escaping, and it does so silently. A `\x1f` intended as the
four-character escape sequence arrives at the interpreter as a single 0x1F
byte and is written into the source as a raw control character. The file still
compiles, the tests still pass, and the byte is invisible in every diff.

Six of them reached `internal/api/cursor_test.go` this way before a byte scan
found them. Nothing was functionally wrong — a raw 0x1F and the escape for it
are the same value to Go — but a control character no reader can see is one an
editor, a linter or a copy-paste will eventually eat, and the failure then
looks like the test being wrong.

**How to avoid it.** Write source files with the editor tool rather than
through a heredoc whenever the content contains a backslash. When a script has
to produce one, build it rather than type it — `chr(92)` in Python — which is
also why this file's own patch scripts do. And after any bulk edit, scan for
control bytes:

```python
bad = [b for b in set(open(path, 'rb').read()) if b < 9 or 13 < b < 32]
```

The same class of problem has now appeared three times in this project in
different costumes: a UTF-16 `.gitignore` that git could not read, CRLF working
files that gofmt rejected while `git status` showed nothing, and now this.
> **Rule.** On this machine, treat any text passing through a shell as
> encoding-unsafe until it has been read back and checked as bytes. The
> checks are cheap; every instance of this has cost an hour.

#### Storage facts worth knowing before reading the code

**`timestamptz` holds microseconds; `time.Time` holds nanoseconds.** An instant
written with nanosecond precision reads back three digits shorter. This is
PostgreSQL's documented resolution rather than a defect, and it is recorded
here because the next reader to meet the `Truncate` call in
`internal/store/convert.go` will ask why it is there.

It is there so the loss happens at one named place instead of inside the
driver, where a value would come back shorter than it went in with nothing
saying why. The call is not falsifiable — removing it leaves everything green,
because the driver truncates identically a moment later — so it is labelled as
buying legibility rather than behaviour, and the label says exactly that, for
whoever eventually decides to delete it.

`OccurredAt` is display-only by the domain's own documentation, which is why
this is a truncation rather than a refusal. NUL in text is refused because a
payee that loses a character stops matching the statement it was copied from;
nobody reading a timestamp is served by sub-microsecond precision, and refusing
it would only mean every caller holding a `time.Now()` truncated first.

#### Coverage is per package, and does not follow a guard across a boundary

`internal/store` went from 72,3% to 70,8% during phase 2, and nothing
regressed. `LoadAccount` and `LoadCommodities` were added to the store while
their guards live in `internal/api`, where the round trip they exist for is
proved end to end against PostgreSQL — a stronger test than a store unit test,
and one the store's own coverage figure cannot see.

Both are genuinely guarded: breaking them turns api tests red, which was
watched rather than assumed. But `go test -coverprofile` attributes a
statement to the package whose *tests* executed it, so a method exercised only
from another package reads as uncovered.

> **Rule.** A coverage number that falls because of attribution is not a
> regression, and the two are indistinguishable from the trend alone. Before
> treating a drop as one, find out which statements stopped being covered and
> by whose tests they were covered before. `go tool cover -func` names them in
> a few seconds; a wrong conclusion drawn from the trend outlives that by a
> milestone.

#### One failure that did not reproduce

While auditing the generator, a break-and-restore cycle ended with
`TestTheLedgerRoutesRefuseAnUnauthenticatedRequest` failing on the restored
tree. Three subsequent runs of the same command were clean, and the file
digests confirmed the restore was complete.

It is recorded as **unexplained** rather than as flakiness, per §11: a green
re-run is not an explanation. The suspected cause is resource contention
between two PostgreSQL containers — `internal/store` starts one and
`internal/api` now starts another, and that cycle had started six of them
within a few minutes on a machine where Docker Desktop has stopped by itself
four times already.

What stopped it being diagnosable is worth more than the guess: **the break
harness captured only the names of failing tests and discarded their output**,
so there was nothing to read afterwards. A harness that records what failed
but not why can tell you something went wrong and never what.

#### Deliberately deferred

| Deferred | Lands in |
| --- | --- |
| Consumption detail in any HTTP response, and with it the wire encoding of `ledger.Rat` | M7 |
| Row ownership on ledger tables, and re-weighing per-account rate limiting — together, when the index falls | M9 |
| `/lots`, balances and reports as endpoints. Balances have five store methods and no consumer; what a report should say is M6's question | M6 |
