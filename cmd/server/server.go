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
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	slogctx "github.com/veqryn/slog-context"
	"log/slog"
	"net/http"
	_ "net/http/pprof"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
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

func run(ctx context.Context, cfg config.ServerConfig) {
	log := slogctx.FromCtx(ctx)

	bs, err := bucketstore.New(cfg.S3Config)
	if err != nil {
		log.Error("run cannot configure s3 storage", slog.Any("error", err))
		os.Exit(1)
	}

	err = bs.Ping(ctx)
	if err != nil {
		log.Error("run cannot ping s3 storage", slog.Any("error", err))
		os.Exit(1)
	}

	authRepository := repository.NewBucketAuthRepository(cfg.APISecretHash, cfg.DefaultRole)
	entryRepository := repository.NewBucketEntryRepository(bs)
	nightscoutRepository := repository.NewNightscoutRepository()

	err = entryRepository.Boot(ctx)
	if err != nil {
		log.Error("run cannot fetch entries", slog.Any("error", err))
	}

	authService := &models.AuthService{AuthRepository: authRepository}

	// TESTING LLU fetch
	cgm := repository.NewCGMLibrelinkupRepository(repository.LLUConfig{
		Region:   strings.ToLower(os.Getenv("LINK_UP_REGION")),
		Username: os.Getenv("LINK_UP_USERNAME"),
		Password: os.Getenv("LINK_UP_PASSWORD"),
	})
	m, err := cgm.FetchRecent(ctx, time.Now())
	if err != nil {
		if cgm.ErrorIsAuthnFailed(err) {
			fmt.Printf("authn failed!\n")
		}
	}
	fmt.Printf("got recent cgm %#v %v", m, err)
	// END OF TESTING

	apiV1C := controllers.ApiV1{
		EntryRepository:      entryRepository,
		NightscoutRepository: nightscoutRepository,
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
		r.With(apiV1mw.Authz("api:entries:create")).Post("/entries", apiV1C.CreateEntries)
		r.With(apiV1mw.Authz("api:entries:create")).Post("/entries/import/nightscout", apiV1C.ImportNightscoutEntries)
		r.With(apiV1mw.Authz("api:entries:read")).Get("/entries", apiV1C.ListEntries)
		r.With(apiV1mw.Authz("api:entries:read")).Get("/entries/{oid:[a-f0-9]{24}}", apiV1C.EntryByOid)
		r.With(apiV1mw.Authz("api:entries:read")).Get("/entries/current", apiV1C.LatestEntry)
	})
	r.Mount("/debug", middleware.Profiler())
	r.Get("/", func(w http.ResponseWriter, r *http.Request) {
		entry, err := entryRepository.FetchLatestSgvEntry(r.Context(), time.Now())
		if err != nil {
			if errors.Is(err, models.ErrNotFound) {
				http.Error(w, "not found", http.StatusNotFound)
				return
			}
			log.Info("entryService.ByID failed", slog.Any("error", err))
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		_, _ = w.Write([]byte(fmt.Sprintf("%#v", entry))) //nolint:errcheck
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
