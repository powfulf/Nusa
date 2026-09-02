# Licensing

Nusa ships under two licences on purpose. This file explains which is which and
why, so nobody has to infer it from file headers.

It also states what the licences do **not** cover: the name and the brand
assets are excluded from both. See *The name and the marks are not licensed*.

## What is licensed how

| Path | Licence | Why |
| --- | --- | --- |
| `internal/ledger/` | **MIT** — [`internal/ledger/LICENSE`](internal/ledger/LICENSE) | The double-entry engine. Reusable by anyone, in anything. |
| The Country Pack specification | **MIT** | An interchange format is only useful if implementing it carries no obligations. |
| Everything else | **AGPL-3.0-only** — [`LICENSE`](LICENSE) | The application itself. |
| The name **Nusa** and the brand assets | **Neither.** Reserved — see below | A licence to the code is not a licence to the identity. |

"Everything else" means the server, the HTTP layer, the persistence layer, the
web frontend, the tooling, and the documentation. It does **not** extend to the
name or the marks, which no licence here grants.

The Country Pack *specification* is the schema and the format — what a pack
must look like for Nusa to load it. The packs distributed in this repository,
such as `plugins/country-pack-id/`, are part of the application and are
AGPL-3.0-only like the rest of it. A pack you write yourself is your own work
and this repository's licences say nothing about it.

## Why the split

**The ledger is MIT because correct double-entry accounting should not be
something each project rebuilds.** Getting money arithmetic right — exact
integer amounts, one rounding site, balanced postings, immutable history,
multi-commodity transactions that balance per commodity — is the part of a
finance application that is genuinely hard and genuinely dangerous to get
wrong. There is no benefit to anyone in that work being reimplemented badly
somewhere else because the licence made reuse awkward. `internal/ledger` has no
dependencies on HTTP, SQL or configuration precisely so it can be lifted out.

**The Country Pack specification is MIT for the same reason a file format
should be.** A format that other tools cannot implement without taking on
licence obligations is a format nobody else implements, and the point of packs
is that anyone can write one for their own country.

**The application is AGPL-3.0 because Nusa is self-hosted software about
money.** A weaker licence would let someone run a modified Nusa as a hosted
service without publishing their changes — and the changes that matter here are
exactly the ones users could not otherwise see: what the software does with
their financial data, whether it phones home, what it added or removed. The
AGPL's network clause is what makes "self-hostable" mean something. Anyone
offering Nusa as a service has to offer its source too.

The two goals do not conflict: the reusable engine is reusable, and the product
stays open to the people running it.

## The name and the marks are not licensed

**The two licences above cover code and documentation. They do not cover the
name "Nusa" or the brand assets, and neither licence grants any right to
either.**

Excluded from both licences and reserved by the copyright holders:

| What | Where |
| --- | --- |
| The product name **Nusa**, and the slug `nusa` used as an identifier | `internal/brand/brand.go` |
| The wordmark | `.github/assets/logo.png` |
| The lettermark | `web/src/assets/logo-letter.png` |
| Any later variant, vector conversion or derivative of those marks | — |

Nothing here restricts what the licences already permit for the code itself.
You may run Nusa, modify it, and distribute your modifications on the AGPL's
terms, or lift `internal/ledger` under MIT. **You may not present the result as
Nusa.**

### What a fork must do

If you distribute a modified version, or run one as a network service:

1. **Change the name.** Pick your own; do not use "Nusa" as the product name, in
   the interface, in the repository name, in a package or image name, or in a
   domain.
2. **Replace the marks.** Remove the files listed above and use your own.
3. **Say what it is derived from, factually.** "Based on Nusa" or "a fork of
   Nusa" is accurate, welcome, and is not a use of the name as your own. What
   is not permitted is anything that presents your build as Nusa itself, or as
   endorsed by or affiliated with it.

None of this is unusual, and none of it is aimed at making forking harder. The
code is the part that is meant to be reused; the name is the part that tells
somebody which project they are actually running, and a name that can mean two
different builds tells them nothing.

### Why this is written down now, while it is cheap

Because the alternative is writing it later, which is expensive and awkward.
Maybe, a comparable open-source personal finance project, had to introduce a
restriction on the use of its name after forks were already carrying it — at
which point the request lands on people who did nothing wrong under the terms
they were given, and the project looks as though it is closing something it had
left open.

Stating it before there is a single fork costs one section in a file nobody has
to argue about. Stating it afterwards costs goodwill that was not necessary to
spend.

**Brand assets are also specified in `DESIGN.md` under Brand assets** — minimum
sizes, clear space, which surfaces they may appear on, and what may never be
done to them. That is a design constraint on *our* use of them, and is separate
from this licence exclusion; a fork replacing the marks is not bound by it,
because the marks are not theirs to be bound about.

### Making the boundary easy to honour

The name is deliberately confined so that renaming is a small change rather
than a search across the tree. `internal/brand/brand.go` is the only place in
Go source that spells it out, and the message catalogues under `web/src/i18n`
are the only other place it appears in shipped code — every user-facing string
goes through i18n rather than hardcoding it. The `NUSA_` environment prefix is
the one deliberate exception, because a variable name cannot be resolved at
runtime; a rename updates it there and in `.env.example`.

The module path `github.com/powfulf/Nusa` is a separate matter again. It tracks
where the source lives, not what the product is called, and a fork changes it
because the repository moved rather than because the name did.

## Per-file identification

**Every hand-written source file carries an SPDX identifier on its first
line.** That is the authoritative statement for that file:

```go
// SPDX-License-Identifier: MIT            // in internal/ledger/
// SPDX-License-Identifier: AGPL-3.0-only  // everywhere else
```

If a file's header and this document ever disagree, the header is what applies
to that file — and the disagreement is a bug worth reporting.

**Generated files are the exception.** The sqlc output in `internal/store`
(`db.go`, `models.go`, `querier.go`, `*.sql.go`) opens with `Code generated by
sqlc. DO NOT EDIT.` and carries no SPDX line. A header added by hand would be
erased by the next `sqlc generate`, and CI checks that regenerating produces no
diff — so the header would not survive its first regeneration and would fail
the build on the way out. Those files are part of `internal/store` and are
therefore AGPL-3.0-only, like everything outside `internal/ledger`.

## Copyright

The `LICENSE` files are the unmodified upstream texts of AGPL-3.0 and MIT.
Nothing has been edited into them, including the copyright line in the AGPL's
closing appendix, which is a template rather than a notice. Modifying licence
text is how automatic licence detection breaks.

Copyright is asserted per file through the SPDX headers, and
`internal/ledger/LICENSE` attributes to *Nusa contributors* rather than to one
person, because as soon as there is a second contributor that is simply what is
true.

## Contributing

Contributions are accepted under the licence of the file being changed. A
change to `internal/ledger` is MIT; a change anywhere else is AGPL-3.0-only.
There is no contributor licence agreement.
