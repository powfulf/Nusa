// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"errors"
	"fmt"
	"strings"

	"github.com/golang-migrate/migrate/v4"

	// Registers the pgx v5 database driver under the "pgx5" URL scheme.
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	"github.com/golang-migrate/migrate/v4/source/iofs"

	appdb "github.com/GaffaQ/Nusa/db"
)

// ErrNoChange reports that the database was already at the requested version.
// It is not a failure — callers usually log it and carry on.
var ErrNoChange = migrate.ErrNoChange

// MigrateUp applies every pending migration.
func MigrateUp(databaseURL string) error {
	m, err := newMigrator(databaseURL)
	if err != nil {
		return err
	}
	defer closeMigrator(m)

	if err := m.Up(); err != nil {
		if errors.Is(err, migrate.ErrNoChange) {
			return ErrNoChange
		}
		return fmt.Errorf("apply migrations: %w", err)
	}
	return nil
}

// MigrateDown rolls back steps migrations, or all of them when steps is zero
// or negative.
//
// This exists for local development. Deployments roll forward.
func MigrateDown(databaseURL string, steps int) error {
	m, err := newMigrator(databaseURL)
	if err != nil {
		return err
	}
	defer closeMigrator(m)

	if steps <= 0 {
		err = m.Down()
	} else {
		err = m.Steps(-steps)
	}

	if err != nil {
		if errors.Is(err, migrate.ErrNoChange) {
			return ErrNoChange
		}
		return fmt.Errorf("roll back migrations: %w", err)
	}
	return nil
}

// MigrateVersion reports the currently applied version. A dirty database
// stopped part-way through a migration and needs manual attention before
// anything else runs.
func MigrateVersion(databaseURL string) (version uint, dirty bool, err error) {
	m, err := newMigrator(databaseURL)
	if err != nil {
		return 0, false, err
	}
	defer closeMigrator(m)

	version, dirty, err = m.Version()
	if err != nil {
		if errors.Is(err, migrate.ErrNilVersion) {
			return 0, false, nil
		}
		return 0, false, fmt.Errorf("read migration version: %w", err)
	}
	return version, dirty, nil
}

func newMigrator(databaseURL string) (*migrate.Migrate, error) {
	source, err := iofs.New(appdb.Migrations, appdb.MigrationsDir)
	if err != nil {
		return nil, fmt.Errorf("open embedded migrations: %w", err)
	}

	m, err := migrate.NewWithSourceInstance("iofs", source, migrateURL(databaseURL))
	if err != nil {
		// The error can echo the connection string, password included.
		return nil, errors.New("open migrator: cannot connect to database")
	}
	return m, nil
}

// migrateURL rewrites the connection string's scheme.
//
// golang-migrate picks its database driver from the URL scheme, and the pgx v5
// driver registers as "pgx5". Rewriting here means operators configure one
// connection string, not two spellings of the same thing.
func migrateURL(databaseURL string) string {
	for _, scheme := range []string{"postgresql://", "postgres://"} {
		if after, found := strings.CutPrefix(databaseURL, scheme); found {
			return "pgx5://" + after
		}
	}
	return databaseURL
}

// closeMigrator releases the migration source and its database connection.
// Both errors are discarded: this only runs after the work is done, and a
// failure to close cannot change whether the migration applied.
func closeMigrator(m *migrate.Migrate) {
	_, _ = m.Close()
}
