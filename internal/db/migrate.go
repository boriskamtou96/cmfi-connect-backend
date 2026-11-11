package db

import (
	"database/sql"
	"errors"
	"fmt"
	"log/slog"

	"github.com/boriskamtou96/cmfi-connect-backend/internal/db/migration"
	"github.com/golang-migrate/migrate/v4"
	migratepostgres "github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/golang-migrate/migrate/v4/source/iofs"
)

// Migrate applies the pending migrations. Two instances starting together
// wait for each other (an advisory lock in Postgres). It uses the same
// schema_migrations table as the migrate tool (make migrate-up), so both
// can be used. A migration that fails leaves the database "dirty": the
// error says so, the server doesn't start, and the previous version keeps
// serving until it is fixed.
func Migrate(conn *sql.DB) error {
	source, err := iofs.New(migration.Files, ".")
	if err != nil {
		return fmt.Errorf("read the migrations: %w", err)
	}
	driver, err := migratepostgres.WithInstance(conn, &migratepostgres.Config{})
	if err != nil {
		return fmt.Errorf("open the migrations table: %w", err)
	}
	migrator, err := migrate.NewWithInstance("iofs", source, "postgres", driver)
	if err != nil {
		return fmt.Errorf("prepare the migrations: %w", err)
	}
	// no migrator.Close(): it would close the connection the server uses
	err = migrator.Up()
	switch {
	case errors.Is(err, migrate.ErrNoChange):
		version, _, _ := migrator.Version()
		slog.Info("database up to date", "version", version)
		return nil
	case err != nil:
		return fmt.Errorf("apply the migrations: %w", err)
	}
	version, _, _ := migrator.Version()
	slog.Info("migrations applied", "version", version)
	return nil
}
