package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
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
		slog.Error("failed to load config", "error", err)
		os.Exit(1)
	}

	// Logs: one JSON line per event in release (read by the host), plain text
	// in development
	slog.SetDefault(newLogger(cfg.Server))

	// Configure the db connection using the loaded configuration
	conn := db.New(cfg.DB)

	// Configure Gin Mode
	gin.SetMode(cfg.Server.GinMode)

	// Configure http server
	store := dbs.NewSQLStore(conn)
	srv, err := api.New(store, cfg)
	if err != nil {
		slog.Error("failed to load server", "error", err)
		os.Exit(1)
	}
	router := srv.SetupRouter()

	// the mobile money payments whose answer was lost are asked again
	reconcileCtx, stopReconciling := context.WithCancel(context.Background())
	defer stopReconciling()
	go srv.ReconcilePayments(reconcileCtx)

	httpServer := &http.Server{
		Addr:         fmt.Sprintf(":%s", cfg.Server.Port),
		Handler:      router,
		ReadTimeout:  requestTimeOut * time.Second,
		WriteTimeout: writeTimeOut * time.Second,
	}

	go func() {
		slog.Info("server running", "port", cfg.Server.Port, "mode", cfg.Server.GinMode)
		if listenServerErr := httpServer.ListenAndServe(); listenServerErr != nil &&
			!errors.Is(listenServerErr, http.ErrServerClosed) {
			slog.Error("failed to start server", "error", listenServerErr)
			os.Exit(1)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	slog.Info("shutting down server")
	ctx, cancel := context.WithTimeout(context.Background(), cancelTimeOut*time.Second)
	defer cancel()

	if shutDownErr := httpServer.Shutdown(ctx); shutDownErr != nil {
		slog.Error("failed to shut down the http server", "error", shutDownErr)
		return
	}
}

// newLogger writes JSON in release mode, text otherwise, from the level of
// LOG_LEVEL (info when unknown).
func newLogger(server utils.ServerConfig) *slog.Logger {
	var level slog.Level
	if err := level.UnmarshalText([]byte(server.LogLevel)); err != nil {
		level = slog.LevelInfo
	}
	options := &slog.HandlerOptions{Level: level}
	if server.GinMode == gin.ReleaseMode {
		return slog.New(slog.NewJSONHandler(os.Stdout, options))
	}
	return slog.New(slog.NewTextHandler(os.Stdout, options))
}
