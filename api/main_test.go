package api

import (
	"database/sql"
	"log"
	"os"
	"testing"

	db "github.com/boriskamtou96/cmfi-connect-backend/internal/db/sqlc"
	"github.com/gin-gonic/gin"
	_ "github.com/lib/pq"
)

const (
	dbDriver = "postgres"
	dsn      = "postgresql://postgres:postgres@localhost:5432/cmfi_connect?sslmode=disable"
)

// testDSN lets a disposable database be used (TEST_DATABASE_URL) instead of
// the development one.
func testDSN() string {
	if value := os.Getenv("TEST_DATABASE_URL"); value != "" {
		return value
	}
	return dsn
}

var testStore *db.SQLStore

func TestMain(m *testing.M) {
	conn, err := sql.Open(dbDriver, testDSN())
	if err != nil {
		log.Fatalln("Cannot connect to database")
	}

	gin.SetMode(gin.TestMode)
	testStore = db.NewSQLStore(conn)

	os.Exit(m.Run())
}
