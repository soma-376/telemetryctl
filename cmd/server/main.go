package main

import (
	"context"
	"log"
	"net/http"

	"github.com/your-org/pulsemetry/internal/server/config"
	"github.com/your-org/pulsemetry/internal/server/database"
	"github.com/your-org/pulsemetry/internal/server/enrollment"
	"github.com/your-org/pulsemetry/internal/server/httpapi"
)

func main() {
	cfg := config.Load()
	db, err := database.OpenPostgres(context.Background(), cfg.PostgresDSN)
	if err != nil {
		log.Fatalf("connect to enrollment database: %v", err)
	}
	defer db.Close()

	repository := enrollment.NewPostgresRepository(db, cfg.InvitePepper)
	service := enrollment.NewService(repository)
	handler := httpapi.NewRouter(service, cfg.Distribution)

	addr := ":" + cfg.Port
	log.Printf("server is running on %s", addr)
	if err := http.ListenAndServe(addr, handler); err != nil {
		log.Fatal(err)
	}
}
