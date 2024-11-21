package main

import (
	"context"
	"errors"
	"fmt"
	repository "github.com/adamlounds/nightscout-go/adapters"
	"github.com/adamlounds/nightscout-go/config"
	"github.com/adamlounds/nightscout-go/controllers"
	"github.com/adamlounds/nightscout-go/models"
	bucketstore "github.com/adamlounds/nightscout-go/stores/bucket"
	postgres "github.com/adamlounds/nightscout-go/stores/postgres"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"os/signal"
	"syscall"
	"time"

	//"github.com/thanos-io/objstore/providers/s3"
	"log/slog"
	"net/http"
	"os"

	slogctx "github.com/veqryn/slog-context"
	//"github.com/thanos-io/objstore/providers/s3"
)

func main() {
	var cfg config.ServerConfig
	err := cfg.RegisterEnv()
	if err != nil {
		panic(err)
	}

	opts := &slog.HandlerOptions{
		Level: cfg.LogLevel,
	}
	h := slogctx.NewHandler(slog.NewJSONHandler(os.Stdout, opts), nil)
	log := slog.New(h)
	slog.SetDefault(log.With(slog.Int("pid", os.Getpid())))
	ctx := slogctx.NewCtx(context.Background(), slog.Default())

	run(ctx, cfg)
}

//https: //github.com/thanos-io/objstore/blob/main/providers/s3

// NewBucketClient creates a new S3 bucket client
//func NewBucketClient(cfg Config, name string, logger log.Logger) (objstore.Bucket, error) {
//	s3Cfg, err := newS3Config(cfg)
//	if err != nil {
//		return nil, err
//	}
//
//	return s3.NewBucketWithConfig(logger, s3Cfg, name, nil)
//}

func run(ctx context.Context, cfg config.ServerConfig) {
	log := slogctx.FromCtx(ctx)

	pg, err := postgres.New(cfg.PSQL)
	if err != nil {
		log.Error("run cannot setup db", slog.Any("error", err))
		os.Exit(1)
	}
	defer pg.Close()

	err = pg.Ping(ctx)
	if err != nil {
		log.Error("run cannot ping db", slog.Any("error", err))
		os.Exit(1)
	}

	b, err := bucketstore.New(cfg.S3Config)
	if err != nil {
		log.Error("run cannot configure s3 storage", slog.Any("error", err))
		os.Exit(1)
	}

	err = b.Ping(ctx)
	if err != nil {
		log.Error("run cannot ping s3 storage", slog.Any("error", err))
		os.Exit(1)
	}

	authRepository := repository.NewPostgresAuthRepository(pg, cfg.APISecretHash, cfg.DefaultRole)
	entryRepository := repository.NewPostgresEntryRepository(pg, b)

	authService := &models.AuthService{AuthRepository: authRepository}

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
		r.With(apiV1mw.Authz("api:entries:create")).Post("/entries", apiV1C.CreateEntries)
		r.Get("/entries/{oid:[a-f0-9]{24}}", apiV1C.EntryByOid)
		r.Get("/entries/current", apiV1C.LatestEntry)
	})
	r.Get("/", func(w http.ResponseWriter, r *http.Request) {
		entry, err := entryRepository.FetchLatestSgvEntry(r.Context())
		if err != nil {
			if errors.Is(err, models.ErrNotFound) {
				http.Error(w, "not found", http.StatusNotFound)
				return
			}
			log.Info("entryService.ByID failed", slog.Any("error", err))
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		w.Write([]byte(fmt.Sprintf("%#v", entry))) //nolint:errcheck
	})

	// TODO look at how to prevent shutdown if s3 writes are in-progress?
	server := &http.Server{Addr: cfg.Server.Address, Handler: r}
	serverCtx, serverStopCtx := context.WithCancel(ctx)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT)
	go func() {
		<-sig
		shutdownCtx, _ := context.WithTimeout(serverCtx, time.Second*10) //nolint:govet
		go func() {
			<-shutdownCtx.Done()
			if errors.Is(shutdownCtx.Err(), context.DeadlineExceeded) {
				log.Error("graceful shutdown timed out, forcing exit")
			}
		}()

		err := server.Shutdown(shutdownCtx)
		if err != nil {
			log.Error("cannot shutdown server", slog.Any("error", err))
		}
		serverStopCtx()
	}()

	log.Info("Starting server on", "address", cfg.Server.Address)
	err = server.ListenAndServe()
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Error("server terminated", slog.Any("error", err))
	}
	log.Info("shutdown ok")
	<-serverCtx.Done()
}
