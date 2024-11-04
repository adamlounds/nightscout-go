package main

import (
	"context"
	"errors"
	"fmt"
	repository "github.com/adamlounds/nightscout-go/adapters"
	"github.com/adamlounds/nightscout-go/config"
	"github.com/adamlounds/nightscout-go/controllers"
	"github.com/adamlounds/nightscout-go/models"
	postgres "github.com/adamlounds/nightscout-go/stores/postgres"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
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

	// /api/v1/entries?count=60&token=ffs-358de43470f328f3

	api1C := controllers.ApiV1{
		EventRepository: eventRepository,
	}

	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.Logger)
	r.Route("/api/v1", func(r chi.Router) {
		r.Get("/entries/", api1C.ListEntries)
		r.Get("/entries/{oid:[a-f0-9]{24}}", api1C.EntryByOid)
		r.Get("/entries/current", api1C.LatestEntry)
	})
	r.Get("/", func(w http.ResponseWriter, r *http.Request) {
		event, err := eventRepository.FetchLatestEvent(r.Context())
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
