# Contributing

Thanks for your interest. This project is pre-alpha; the fastest way to help right
now is to try the setup below and report anything that does not work.

## Getting set up

You need Go 1.22+, Node 20+ and Docker.

```bash
cp .env.example .env
make dev
```

`make dev` starts Postgres, applies migrations, runs the API and the Vite dev
server. `make test` and `make lint` must both pass before you open a pull request.

## Ground rules

The repository root contains a normative project guide covering architecture,
money handling, ledger invariants, UI language and the design system. Read it
before writing code. A change that conflicts with it will not be merged — if you
think the guide is wrong, open an issue about the guide first.

The rules most likely to trip you up:

- **Never use floating point for money.** Not `float64`, not JavaScript `number`,
  not `numeric` with decimal places in Postgres. Money is an integer count of the
  commodity's smallest unit, always paired with a commodity code.
- **`internal/ledger` is pure domain.** It must not import `database/sql`,
  `net/http`, or any config package. If it seems to need one, the design is wrong.
- **No hardcoded user-facing strings.** Everything goes through i18n, with both
  English and Indonesian catalogs updated in the same change.
- **Postings are immutable.** Corrections create reversing entries; nothing
  mutates history and nothing is hard-deleted.

## Pull requests

- [Conventional Commits](https://www.conventionalcommits.org/) for commit messages.
- Every bug fix starts with a failing test that reproduces the bug.
- Every user-facing change updates `docs/`.
- New dependencies need a note in the PR explaining why the standard library is
  insufficient.
- Wrap errors with `fmt.Errorf("...: %w", err)`. Never `panic` outside `main`.
- Comments explain *why*. If *what* is unclear, rename things instead.

## Country Packs

Everything country-specific belongs in a Country Pack plugin, never in core.
Country Packs are mostly declarative YAML and are designed to be contributed by
people who do not write Go. This is the single most valuable place to contribute
if you want Nusa to work in your country.

## Code of Conduct

Be decent to each other. Harassment, personal attacks and bad-faith participation
get you removed. Report problems to the address in [SECURITY.md](SECURITY.md).
