package db

import (
	_ "github.com/lib/pq"
	"database/sql"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

// MIGRATE_TEST_DSN: an empty disposable database. Without it the test is skipped.
func TestMigrateAppliesEveryMigrationOnAnEmptyDatabaseThenNothing(t *testing.T) {
	dsn := os.Getenv("MIGRATE_TEST_DSN")
	if dsn == "" {
		t.Skip("MIGRATE_TEST_DSN is not set")
	}
	conn, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	require.NoError(t, Migrate(conn))
	var version int
	var dirty bool
	require.NoError(t, conn.QueryRow(`SELECT version, dirty FROM schema_migrations`).Scan(&version, &dirty))
	require.False(t, dirty)
	require.GreaterOrEqual(t, version, 40)
	var nudges int
	require.NoError(t, conn.QueryRow(`SELECT COUNT(*) FROM disciple_nudges`).Scan(&nudges))

	// started again: nothing to do
	require.NoError(t, Migrate(conn))
}
