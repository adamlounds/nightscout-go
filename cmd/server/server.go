package main

import (
	"context"
	"errors"
	"fmt"
	"github.com/adamlounds/nightscout-go/models"
	"github.com/go-chi/chi/v5"
	"net/http"
	"os"
)

type config struct {
	PSQL   models.PostgresConfig
	Server struct {
		Address string
	}
}

func loadEnvConfig() (config, error) {
	var cfg config
	cfg.PSQL = models.PostgresConfig{
		Host:     os.Getenv("POSTGRES_HOST"),
		Port:     os.Getenv("POSTGRES_PORT"),
		User:     os.Getenv("POSTGRES_USER"),
		Password: os.Getenv("POSTGRES_PASSWORD"),
		Database: os.Getenv("POSTGRES_DB"),
		SSLMode:  os.Getenv("POSTGRES_SSLMODE"),
	}
	cfg.Server.Address = os.Getenv("SERVER_ADDRESS")
	return cfg, nil
}

func main() {
	cfg, err := loadEnvConfig()
	if err != nil {
		panic(err)
	}

	err = run(cfg)
	if err != nil {
		panic(err)
	}
}

func run(cfg config) error {
	dbpool, err := models.Connect(context.Background(), cfg.PSQL)
	if err != nil {
		return fmt.Errorf("run cannot connect to db: %w", err)
	}
	defer dbpool.Close()

	var pgVersion string
	err = dbpool.QueryRow(context.Background(), "select version()").Scan(&pgVersion)
	if err != nil {
		fmt.Fprintf(os.Stderr, "pgVersion failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("pg connected [%s]\n", pgVersion)

	eventService := &models.EventService{
		DB: dbpool,
	}

	r := chi.NewRouter()
	r.Get("/", func(w http.ResponseWriter, r *http.Request) {
		event, err := eventService.ByID(r.Context(), 1)
		if err != nil {
			if errors.Is(err, models.ErrNotFound) {
				http.Error(w, "not found", http.StatusNotFound)
				return
			}
			fmt.Printf("eventService.ByID failed: %v\n", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		w.Write([]byte(fmt.Sprintf("%#v", event)))
	})
	fmt.Printf("Starting server on [%s]\n", cfg.Server.Address)
	return http.ListenAndServe(cfg.Server.Address, r)
}
