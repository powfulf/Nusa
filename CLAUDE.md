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
motion curve, icon and component specification originates there and nowhere
else.

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
