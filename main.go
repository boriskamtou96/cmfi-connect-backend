package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/boriskamtou96/cmfi-connect-backend/api"
	"github.com/boriskamtou96/cmfi-connect-backend/internal/db"
	"github.com/boriskamtou96/cmfi-connect-backend/internal/utils"
	"github.com/gin-gonic/gin"
)

const (
	requestTimeOut = 30
	writeTimeOut   = 30
	cancelTimeOut  = 60
)

func main() {
	// Load configuration
	cfg, err := utils.LoadConfig()
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	// Configure the db connection using the loaded configuration
	db, err := db.New(cfg.DB)
	if err != nil {
		log.Fatalf("Failed to connect to db: %v", err)
	}
	mainDB, err := db.DB()
	if err != nil {
		log.Fatalf("Failed to get db connection: %v", err)
	}
	defer func(mainDB *sql.DB) {
		err := mainDB.Close()
		if err != nil {
			log.Printf("Failed to close db connection: %v", err)
		}
	}(mainDB)

	// Configure Gin Mode
	gin.SetMode(cfg.Server.GinMode)

	// Configure http server
	srv := api.New(db, cfg)
	router := srv.SetupRouter()

	httpServer := &http.Server{
		Addr:         fmt.Sprintf(":%s", cfg.Server.Port),
		Handler:      router,
		ReadTimeout:  requestTimeOut * time.Second,
		WriteTimeout: writeTimeOut * time.Second,
	}

	go func() {
		log.Printf("Server running on port: %s", cfg.Server.Port)
		if listenServerErr := httpServer.ListenAndServe(); listenServerErr != nil &&
			!errors.Is(listenServerErr, http.ErrServerClosed) {
			log.Fatal("failed to start server")
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("shutting down server")
	ctx, cancel := context.WithTimeout(context.Background(), cancelTimeOut*time.Second)
	defer cancel()

	if shutDownErr := httpServer.Shutdown(ctx); shutDownErr != nil {
		fmt.Println("failed to shutdown http server....")
		return
	}
}
