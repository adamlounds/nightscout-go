package main

import (
	"context"
	"errors"
	"fmt"
	repository "github.com/adamlounds/nightscout-go/adapters"
	"github.com/adamlounds/nightscout-go/config"
	"github.com/adamlounds/nightscout-go/models"
	postgres "github.com/adamlounds/nightscout-go/stores/postgres"
	"github.com/go-chi/chi/v5"
	"net/http"
	"os"
)

func loadEnvConfig() (config.ServerConfig, error) {
	var cfg config.ServerConfig
	cfg.PSQL = postgres.PostgresConfig{
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

	err = run(context.Background(), cfg)
	if err != nil {
		panic(err)
	}
}

func run(ctx context.Context, cfg config.ServerConfig) error {
	pg, err := postgres.New(cfg.PSQL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "run cannot setup db: %s\n", err)
		os.Exit(1)
	}
	defer pg.Close()

	err = pg.Ping(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "run cannot ping db: %s\n", err)
		os.Exit(1)
	}

	eventRepository := repository.NewPostgresEventRepository(pg)

	r := chi.NewRouter()
	r.Get("/", func(w http.ResponseWriter, r *http.Request) {
		event, err := eventRepository.FetchEvent(r.Context(), 1)
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
