package db

import (
	"database/sql"
	"log/slog"
	"os"

	"github.com/boriskamtou96/cmfi-connect-backend/internal/utils"

	_ "github.com/lib/pq"
)

// New creates a new GORM db connection using the provided db configuration.
func New(dbConfig utils.DBConfig) *sql.DB {
	conn, err := sql.Open(dbConfig.Driver, dbConfig.Dsn)
	if err != nil {
		slog.Error("cannot connect to the database", "error", err)
		os.Exit(1)
	}
	if err = conn.Ping(); err != nil {
		slog.Error("cannot reach the database", "error", err)
		os.Exit(1)
	}

	return conn
}
