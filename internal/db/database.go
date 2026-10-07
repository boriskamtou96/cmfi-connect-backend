package db

import (
	"database/sql"
	"log"

	"github.com/boriskamtou96/cmfi-connect-backend/internal/utils"

	_ "github.com/lib/pq"
)

// New creates a new GORM db connection using the provided db configuration.
func New(dbConfig utils.DBConfig) *sql.DB {
	conn, err := sql.Open(dbConfig.Driver, dbConfig.Dsn)
	if err != nil {
		log.Fatal("cannot connect to db:", err)
	}
	if err = conn.Ping(); err != nil {
		log.Fatal("cannot ping db:", err)
	}

	return conn
}
