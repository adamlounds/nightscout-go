package main

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"os"

	repository "github.com/adamlounds/nightscout-go/adapters"
	"github.com/adamlounds/nightscout-go/config"
	"github.com/adamlounds/nightscout-go/controllers"
	"github.com/adamlounds/nightscout-go/models"
	postgres "github.com/adamlounds/nightscout-go/stores/postgres"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
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

	// Create SHA1 hash of API_SECRET
	apiSecret := os.Getenv("API_SECRET")
	hasher := sha1.New()
	hasher.Write([]byte(apiSecret))
	cfg.APISecretHash = hex.EncodeToString(hasher.Sum(nil))
	cfg.DefaultRole = os.Getenv("DEFAULT_ROLE")
	if cfg.DefaultRole == "" {
		cfg.DefaultRole = "readable"
	}

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

	authRepository := repository.NewPostgresAuthRepository(pg, cfg.APISecretHash, cfg.DefaultRole)
	entryRepository := repository.NewPostgresEntryRepository(pg)

	authService := &models.AuthService{authRepository}

	// /api/v1/entries?count=60&token=ffs-358de43470f328f3

	apiV1C := controllers.ApiV1{
		EntryRepository: entryRepository,
	}
	apiV1mw := controllers.ApiV1AuthnMiddleware{
		AuthService: authService,
	}

	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.Logger)
	r.Use(middleware.StripSlashes)
	r.Route("/api/v1", func(r chi.Router) {
		r.Use(apiV1mw.SetAuthentication)
		r.Use(middleware.URLFormat)
		r.With(apiV1mw.Authz("api:entries:read")).Get("/entries", apiV1C.ListEntries)
		//r.With(apiV1mw.SetAuthentication).Get("/entries/", apiV1C.ListEntries)
		r.Get("/entries/{oid:[a-f0-9]{24}}", apiV1C.EntryByOid)
		r.Get("/entries/current", apiV1C.LatestEntry)
	})
	r.Get("/", func(w http.ResponseWriter, r *http.Request) {
		entry, err := entryRepository.FetchLatestEntry(r.Context())
		if err != nil {
			if errors.Is(err, models.ErrNotFound) {
				http.Error(w, "not found", http.StatusNotFound)
				return
			}
			fmt.Printf("entryService.ByID failed: %v\n", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		w.Write([]byte(fmt.Sprintf("%#v", entry)))
	})
	fmt.Printf("Starting server on [%s]\n", cfg.Server.Address)
	return http.ListenAndServe(cfg.Server.Address, r)
}
