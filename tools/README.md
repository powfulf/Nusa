# Build tools

Development tools live in their own Go module, pinned with `tool` directives in
`tools/go.mod`.

They are kept out of the application module on purpose. `golangci-lint` and
`sqlc` require far newer Go versions than the server does, and they drag in
hundreds of dependencies. Pinning them in the root `go.mod` would mean the
minimum Go version a contributor needs to *build the server* is dictated by the
*linter*, and that `go.sum` for the application is dominated by tooling.

Pinning them here instead means everyone — CI included — runs the same tool
versions, upgrading a tool is one commit that cannot touch the application's
dependency graph, and the application's Go floor is set by the application's
own dependencies.

The Makefile builds these into `bin/` and runs them from the repository root:

```bash
make generate   # sqlc
make lint-go    # golangci-lint
make fmt        # golangci-lint fmt
```

After changing a tool version, tidy this module — tools share dependencies, and
adding one can leave another's `go.sum` entries incomplete:

```bash
go -C tools mod tidy
```

Nothing here is imported by the application, and this module is excluded from
the Docker build.
