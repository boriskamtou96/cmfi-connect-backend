package main

import (
	"database/sql"
	"log"

	"github.com/boriskamtou96/cmfi-connect-backend/internal/config"
	"github.com/boriskamtou96/cmfi-connect-backend/internal/database"
	"github.com/gin-gonic/gin"
)

func main() {
	// Load configuration
	cfg, err := config.LoadConfig()
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	// Configure the database connection using the loaded configuration
	db, err := database.New(cfg.DB)
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	mainDB, err := db.DB()
	if err != nil {
		log.Fatalf("Failed to get database connection: %v", err)
	}
	defer func(mainDB *sql.DB) {
		err := mainDB.Close()
		if err != nil {
			log.Printf("Failed to close database connection: %v", err)
		}
	}(mainDB)

	// Configure Gin Mode
	gin.SetMode(cfg.Server.GinMode)
}
