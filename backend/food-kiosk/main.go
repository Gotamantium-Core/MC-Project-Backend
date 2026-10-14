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

	"food-kiosk/backend"
	"food-kiosk/database"
	"food-kiosk/mqtt"
	"food-kiosk/server"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

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

	queues, err := backend.New(ctx, db)
	if err != nil {
		log.Fatal(err)
	}
	client, err := mqtt.New(ctx, mqtt.Config{
		Broker:   server.Getenv("MQTT_BROKER", "tcp://127.0.0.1:1883"),
		ClientID: server.Getenv("MQTT_CLIENT_ID", "canteen-backend"),
		Username: os.Getenv("MQTT_USERNAME"), Password: os.Getenv("MQTT_PASSWORD"),
	}, queues, log.Default())
	if err != nil {
		log.Fatal(err)
	}
	go func() {
		log.Println("canteen MQTT service running")
		if err := client.Run(); err != nil {
			log.Fatalf("mqtt: %v", err)
		}
	}()

	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           server.NewRouter(cfg, db),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	// Run the server in the background so the main goroutine can wait for a
	// signal and shut them both down cleanly.
	go func() {
		log.Printf("food-kiosk server listening on %s (auth required: %t)", cfg.Addr, cfg.AuthRequired)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("server: %v", err)
		}
	}()

	<-ctx.Done()
	log.Println("shutting down...")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("graceful shutdown failed: %v", err)
	}
	log.Println("server stopped")
}
