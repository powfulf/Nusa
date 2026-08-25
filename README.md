<p align="center">
  <img src=".github/assets/logo.png" alt="Nusa wordmark" width="320">
</p>

# Nusa

A self-hostable, open-source personal finance app: one correct double-entry ledger
underneath, with budgeting, investments and planning built on top.

> **Status: pre-alpha.** This repository currently contains the project skeleton only
> (milestone M0). There is no financial logic yet — see [Roadmap](#roadmap).

`Nusa` is a codename. The product name is not final.

## Why

Most personal finance tools pick two of these four. Nusa aims for all four:

1. Correct double-entry accounting
2. Envelope budgeting ordinary people can actually use
3. First-class multi-currency and multi-asset support
4. Usable in any country, without bank APIs

Nusa never asks for bank or broker credentials, never executes trades or payments,
and never gives financial advice. It is a record-keeping and planning tool.

## Try it

Requires Docker.

```bash
cp .env.example .env && docker compose up
```

Then open http://localhost:5173 for the web app, or check the API directly:

```bash
curl http://localhost:8080/healthz
```

## Development

Requires Go 1.22+, Node 20+ and Docker.

```bash
make dev
```

Other targets: `make test`, `make lint`, `make generate`, `make migrate-up`,
`make migrate-down`. Run `make help` for the full list.

## Layout

| Path | Contents |
| --- | --- |
| `cmd/nusa/` | Server entrypoint |
| `internal/ledger/` | The domain core: money, accounts, transactions, lots |
| `internal/` | Budgeting, scheduling, rules, investments, plugins, API, storage |
| `db/` | Migrations and sqlc queries |
| `web/` | React frontend |
| `docs/` | Documentation site |

## Roadmap

M0 bootstrap · **M1 ledger core** · M2 persistence and API · M3 design system ·
M4 core flows · M5 envelopes and rules · M6 multi-currency and reports ·
M7 investments · M8 country packs · M9 household · M10 scenarios ·
M11 AI layer · M12 release readiness.

## License

AGPL-3.0-only, except `internal/ledger/` and the Country Pack specification,
which are MIT so that the accounting core can be reused freely.

**[LICENSING.md](LICENSING.md) explains the split and why it is drawn there.**
Full texts: [LICENSE](LICENSE) (AGPL-3.0-only) and
[internal/ledger/LICENSE](internal/ledger/LICENSE) (MIT). Every source file
carries an SPDX identifier, which is authoritative for that file.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md). Security issues: [SECURITY.md](SECURITY.md).
