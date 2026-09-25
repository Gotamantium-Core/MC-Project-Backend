package main

import (
	"log"

	"food-kiosk/database"
)

func main() {
	db, err := database.Open("food-kiosk.db")
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	if err := database.Initialize(db); err != nil {
		log.Fatal(err)
	}

	log.Println("Database is ready")
}
