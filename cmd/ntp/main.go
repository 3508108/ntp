// NTP Dashboard — Go point of entry.
//
// Структура запуску:
//  1. Зчитати конфіг із середовища
//  2. Відкрити SQLite (modernc.org/sqlite)
//  3. Під'єднати SSE-шину та Sampler
//  4. Запустити HTTP-сервер на Config.ListenAddr
//  5. Обробити SIGTERM/SIGINT → GracefulStop sampler → Shutdown server
package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/karpenkodima/ntp-dashboard/internal/config"
	"github.com/karpenkodima/ntp-dashboard/internal/sampler"
	"github.com/karpenkodima/ntp-dashboard/internal/server"
	"github.com/karpenkodima/ntp-dashboard/internal/store"
)

func main() {
	cfg := config.Load()

	st, err := store.Open(cfg.DBPath)
	if err != nil {
		log.Fatalf("open store: %v", err)
	}
	defer st.Close()

	bus := server.NewSSEBus()
	smp := sampler.New(st, cfg.DBPath, cfg.IntervalMin, cfg.IntervalMax)
	smp.Start()
	defer func() {
		smp.GracefulStop(12*time.Second, 2*time.Second)
	}()

	server.SetDashboard(dashboardBytes)

	router := server.NewRouter(server.Deps{
		Sampler: smp,
		Store:   st,
		DBPath:  cfg.DBPath,
		Bus:     bus,
	})

	srv := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           router,
		ReadHeaderTimeout: 30 * time.Second,
	}

	go func() {
		log.Printf("NTP Dashboard → http://localhost%s", cfg.ListenAddr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("listen: %v", err)
		}
	}()

	waitForSignal(srv, smp)
}

func waitForSignal(srv *http.Server, smp *sampler.Sampler) {
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGTERM, syscall.SIGINT)
	<-stop

	log.Printf("shutdown: graceful stop...")

	// 1) зупинити sampler, щоб уникнути хибних downtime
	smp.GracefulStop(12*time.Second, 2*time.Second)

	// 2) дочекатися завершення HTTP-запитів
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Printf("http shutdown: %v", err)
	}
	log.Printf("shutdown: done")
}
