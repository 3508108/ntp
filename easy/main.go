package main

import (
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"ntp/easy/internal/fetcher"
	"ntp/easy/internal/server"
	"ntp/easy/internal/store"
)

func main() {
	dbPath := os.Getenv("EASY_DB")
	if dbPath == "" {
		dbPath = "easy.db"
	}

	intervalStr := os.Getenv("EASY_INTERVAL")
	interval := 10 * time.Second
	if intervalStr != "" {
		if d, err := time.ParseDuration(intervalStr); err == nil {
			interval = d
		}
	}

	db, err := store.Open(dbPath)
	if err != nil {
		log.Fatalf("open db: %v", err)
	}
	defer db.Close()

	f := fetcher.New(db, interval)
	f.Start()
	defer f.Stop()

	port := os.Getenv("EASY_PORT")
	if port == "" {
		port = "8080"
	}

	srv := server.New(db, f)
	go func() {
		addr := ":" + port
		log.Printf("listening on %s", addr)
		if err := srv.Run(addr); err != nil && err != http.ErrServerClosed {
			log.Fatalf("server: %v", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("shutting down...")
	if err := srv.Shutdown(); err != nil {
		log.Printf("shutdown: %v", err)
	}
}
