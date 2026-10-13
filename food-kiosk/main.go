package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"food-kiosk/database"
	"food-kiosk/server"
)

func main() {
	cfg, err := server.LoadConfig()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	db, err := database.Open(cfg.DBPath)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	if err := database.Initialize(db); err != nil {
		log.Fatal(err)
	}

	if cfg.AdminPrincipal != "" {
		if err := server.BootstrapAdmin(context.Background(), db, cfg.AdminPrincipal, cfg.AdminPassword); err != nil {
			log.Fatalf("bootstrap admin: %v", err)
		}
		log.Printf("admin credential %q is ready", cfg.AdminPrincipal)
	}

	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           server.NewRouter(cfg, db),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	// Run the server in the background so the main goroutine can wait for a
	// signal and shut it down cleanly.
	go func() {
		log.Printf("food-kiosk server listening on %s (auth required: %t)", cfg.Addr, cfg.AuthRequired)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("server: %v", err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop
	log.Println("shutting down...")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Printf("graceful shutdown failed: %v", err)
	}
	log.Println("server stopped")
}
