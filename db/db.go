// SPDX-License-Identifier: AGPL-3.0-only

// Package db carries the SQL migrations as embedded files.
//
// Embedding rather than reading from disk means the binary and the schema it
// expects can never disagree: there is no image to keep in step, no volume to
// mount, and no way to deploy a container whose migration files are from a
// different build.
//
// Migrations are forward-only. Down migrations exist for local development and
// are not part of any deployment path.
package db

import "embed"

// Migrations holds every migration file, named as golang-migrate expects:
// <version>_<name>.up.sql and <version>_<name>.down.sql.
//
//go:embed migrations/*.sql
var Migrations embed.FS

// MigrationsDir is the path of the migration files inside Migrations.
const MigrationsDir = "migrations"
