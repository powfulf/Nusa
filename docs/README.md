# Documentation

This directory holds the VitePress documentation site: installation, user
guide, how to write a Country Pack, API reference, architecture and deployment.

It is scaffolded here from milestone M0 so that the working convention "every
user-facing change updates `docs/`" has somewhere to land from the first
feature. The site itself is built in milestone M12.

Until then, the documents that matter live at the repository root:

- `README.md` — what this is and how to run it
- `CONTRIBUTING.md` — how to work on it
- `SECURITY.md` — how to report a vulnerability

## The HTTP API

As of M2b Phase 2 there is one, and **the specification is served by the
running instance rather than written out here**:

```
GET /openapi.json
```

That is deliberate and is the whole of the API reference until M12. A copy of
the surface in prose would be a second description of it, free to drift from
the first — and this project has spent a milestone learning what a second
description costs. The served document is checked against the router in both
directions on every test run: every route that exists appears in it, and every
path in it exists in the router.

What the specification will not tell you, because it describes shapes rather
than reasons, is why the surface has the shape it does. Those decisions are
recorded in `CLAUDE.md` §13 under *M2b Phase 2*. The three most likely to
surprise somebody reading the endpoints for the first time:

- **A transaction is never edited.** There is no `PUT` and no `PATCH` on a
  transaction or a posting, and no `DELETE` anywhere. A correction and a
  deletion are new transactions that answer the old one, written through
  `POST /api/v1/transactions/{id}/corrections` and `.../deletions`. The
  journal is append-only, so a "delete" is a tombstone and history stays
  readable.

- **Money crosses the wire as a string.** `{"amount": "1500000", "commodity":
  "IDR"}`, counted in the commodity's smallest units, never as a JSON number.
  JavaScript loses precision above 2^53, and a balance wrong by one unit is
  indistinguishable from theft a year later. Parse it with `BigInt`.

- **Every mutation requires an `Idempotency-Key`**, and the server never
  invents one. A replay returns the first attempt's status and body exactly;
  the same key with a different request is refused as a conflict rather than
  answered with the first result.

## Running an instance

`README.md` at the root covers this. In short: `docker compose up`, then
`nusa migrate up` to apply the schema — `serve` never migrates implicitly, so
rolling out a binary and changing the schema stay separate acts.

Registration is open until the first account exists and closed afterwards,
until invitations arrive in M9. Setting up a second factor is optional and
lives under `/api/v1/auth/totp`; the secret and the backup codes are each
shown exactly once, when they are issued, and no endpoint reads them back.
