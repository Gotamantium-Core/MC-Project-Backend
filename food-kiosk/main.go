package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"food-kiosk/backend"
	"food-kiosk/database"
	"food-kiosk/mqtt"
)

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	db, err := database.Open(env("DATABASE_PATH", "food-kiosk.db"))
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()
	if err := database.Initialize(db); err != nil {
		log.Fatal(err)
	}
	queues, err := backend.New(ctx, db)
	if err != nil {
		log.Fatal(err)
	}
	client, err := mqtt.New(ctx, mqtt.Config{
		Broker:   env("MQTT_BROKER", "tcp://127.0.0.1:1883"),
		ClientID: env("MQTT_CLIENT_ID", "canteen-backend"),
		Username: os.Getenv("MQTT_USERNAME"), Password: os.Getenv("MQTT_PASSWORD"),
	}, queues, log.Default())
	if err != nil {
		log.Fatal(err)
	}
	log.Println("Database ready; starting canteen MQTT service")
	if err := client.Run(); err != nil {
		log.Fatal(err)
	}
}
