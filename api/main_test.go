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

var testStore *db.SQLStore

func TestMain(m *testing.M) {
	conn, err := sql.Open(dbDriver, dsn)
	if err != nil {
		log.Fatalln("Cannot connect to database")
	}

	gin.SetMode(gin.TestMode)
	testStore = db.NewSQLStore(conn)

	os.Exit(m.Run())
}
