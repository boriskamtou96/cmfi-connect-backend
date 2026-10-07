package db

import (
	"database/sql"
	"log"
	"os"
	"testing"

	_ "github.com/lib/pq"
)

const (
	dbDriver = "postgres"
	dsn      = "postgresql://postgres:postgres@localhost:5432/cmfi_connect?sslmode=disable"
)

var testQueries *Queries

func TestMain(m *testing.M) {
	conn, err := sql.Open(dbDriver, dsn)
	if err != nil {
		log.Fatalln("Cannot connect to database")
	}

	testQueries = New(conn)

	os.Exit(m.Run())
}
