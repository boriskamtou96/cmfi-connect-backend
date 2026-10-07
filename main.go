package main

import (
	"context"
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
	dbs "github.com/boriskamtou96/cmfi-connect-backend/internal/db/sqlc"
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
	conn := db.New(cfg.DB)

	// Configure Gin Mode
	gin.SetMode(cfg.Server.GinMode)

	// Configure http server
	store := dbs.NewSQLStore(conn)
	srv, err := api.New(store, cfg)
	if err != nil {
		log.Fatalf("Failed to load server: %v", err)
	}
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
