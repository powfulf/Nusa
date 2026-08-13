// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Store is the entry point to persistence. It embeds the sqlc-generated
// Queries, so callers reach generated query methods directly while connection
// lifecycle stays here.
type Store struct {
	*Queries

	pool *pgxpool.Pool
}

// Open connects to PostgreSQL and verifies the connection before returning.
//
// Verifying eagerly is deliberate: a process that cannot reach its database is
// not healthy, and finding that out at startup is far better than finding out
// during a user's first write.
func Open(ctx context.Context, databaseURL string) (*Store, error) {
	poolCfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		// The parse error can echo the connection string, password included,
		// so it is deliberately not wrapped here.
		return nil, fmt.Errorf("parse database url: invalid connection string")
	}

	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, fmt.Errorf("create connection pool: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}

	return &Store{Queries: New(pool), pool: pool}, nil
}

// Close releases the connection pool. It is safe to call once, at shutdown.
func (s *Store) Close() { s.pool.Close() }

// Pool exposes the underlying pool for the few callers that need transaction
// control beyond a single generated query.
func (s *Store) Pool() *pgxpool.Pool { return s.pool }

// CheckDatabase reports the schema version recorded in the database, which
// doubles as proof that the connection works and that migrations have run.
//
// It satisfies the health-check interface declared by the api package.
func (s *Store) CheckDatabase(ctx context.Context) (int32, error) {
	version, err := s.GetSchemaVersion(ctx)
	if err != nil {
		return 0, fmt.Errorf("read schema version: %w", err)
	}
	return version, nil
}
