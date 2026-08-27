package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/cutmax/cutmax-backend/internal/config"
	"github.com/cutmax/cutmax-backend/internal/db"
	"github.com/cutmax/cutmax-backend/internal/router"
)

func main() {
	if err := config.LoadConfig(); err != nil {
		log.Fatalf("config: %v", err)
	}

	// Connect to Postgres
	var err error
	var pool *pgxpool.Pool
	ctx := context.Background()
	for i := 0; i < 30; i++ {
		pool, err = pgxpool.New(ctx, config.Cfg.DatabaseURL)
		if err == nil {
			if err = pool.Ping(ctx); err == nil {
				break
			}
		}
		time.Sleep(time.Second)
	}
	if err != nil {
		log.Fatalf("database: %v", err)
	}
	defer pool.Close()
	db.Pool = pool

	// Ensure uploads dir
	os.MkdirAll(config.Cfg.UploadsDir, 0755)

	// Build router
	r := router.New()

	// Start server
	addr := fmt.Sprintf(":%d", config.Cfg.Port)
	log.Printf("CutMax Go backend listening on %s", addr)
	srv := &http.Server{Addr: addr, Handler: r, ReadTimeout: 30 * time.Second, WriteTimeout: 60 * time.Second}

	// Graceful shutdown
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh
		log.Println("shutting down...")
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		srv.Shutdown(ctx)
	}()

	if err := srv.ListenAndServe(); err != http.ErrServerClosed {
		log.Fatalf("server: %v", err)
	}
	log.Println("stopped")
}
