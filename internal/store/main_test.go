// SPDX-License-Identifier: AGPL-3.0-only

package store_test

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/GaffaQ/Nusa/internal/store"
)

// These tests run against a real PostgreSQL, started here and thrown away
// afterwards. Not a mock, and not a shared development database: a mock would
// be testing the mock, and a shared database would let one run's leftovers
// decide another run's result.
//
// There is no build tag. A tagged integration suite is one that a green
// `go test ./...` says nothing about, and "the tests pass" would quietly come
// to mean "the tests that ran passed". If Docker is missing these fail loudly,
// which is the honest outcome for a project whose only supported database is
// PostgreSQL and whose only supported deployment is a container.

var databaseURL string

func TestMain(m *testing.M) {
	ctx := context.Background()

	container, err := tcpostgres.Run(ctx, "postgres:16",
		tcpostgres.WithDatabase("nusa"),
		tcpostgres.WithUsername("nusa"),
		tcpostgres.WithPassword("test-only-password"),
		testcontainers.WithWaitStrategy(
			// Postgres logs readiness twice: once for the bootstrap server the
			// entrypoint runs to create the database, and once for the real
			// one. Waiting for the first would connect to a server that is
			// about to be shut down.
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(2*time.Minute),
		),
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "start postgres container: %v\n", err)
		fmt.Fprintf(os.Stderr, "these are integration tests and need a running Docker daemon\n")
		os.Exit(1)
	}

	databaseURL, err = container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		fmt.Fprintf(os.Stderr, "connection string: %v\n", err)
		os.Exit(1)
	}

	if err := store.MigrateUp(databaseURL); err != nil {
		fmt.Fprintf(os.Stderr, "migrate: %v\n", err)
		os.Exit(1)
	}

	code := m.Run()

	if err := testcontainers.TerminateContainer(container); err != nil {
		fmt.Fprintf(os.Stderr, "terminate container: %v\n", err)
	}
	os.Exit(code)
}

// open returns a Store on the shared container, with the ledger tables emptied
// so one test cannot decide another's result. Commodities survive: they are
// reference data written by a migration, not test fixtures.
func open(t *testing.T) *store.Store {
	t.Helper()

	ctx := context.Background()
	s, err := store.Open(ctx, databaseURL)
	require.NoError(t, err, "open store")
	t.Cleanup(s.Close)

	_, err = s.Pool().Exec(ctx,
		`TRUNCATE audit_log, idempotency_keys, lot_consumptions, lots, postings, transactions, accounts,
		          sessions, user_totp, user_backup_codes, users`)
	require.NoError(t, err, "reset tables")

	return s
}

// testID builds a canonical lowercase UUIDv7 from a label, so a test's
// identities are stable across runs and readable in a failure message.
//
// Identities always come from outside the domain and outside the store; tests
// are just another outside.
func testID(label string) string {
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
