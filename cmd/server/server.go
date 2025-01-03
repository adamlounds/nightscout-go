package main

import (
	"context"
	"errors"
	repository "github.com/adamlounds/nightscout-go/adapters"
	"github.com/adamlounds/nightscout-go/config"
	"github.com/adamlounds/nightscout-go/controllers"
	"github.com/adamlounds/nightscout-go/models"
	bucketstore "github.com/adamlounds/nightscout-go/stores/bucket"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	socketio "github.com/googollee/go-socket.io"
	"github.com/googollee/go-socket.io/engineio"
	"github.com/googollee/go-socket.io/engineio/transport"
	"github.com/googollee/go-socket.io/engineio/transport/polling"
	"github.com/oklog/ulid/v2"
	slogctx "github.com/veqryn/slog-context"
	"log/slog"
	"net/http"
	_ "net/http/pprof"
	"os"
	"os/signal"
	"path/filepath"
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
	serverCtx, serverStopCtx := context.WithCancel(ctx)

	bs, err := bucketstore.New(cfg.S3Config)
	if err != nil {
		log.Error("run cannot configure s3 storage", slog.Any("error", err))
		os.Exit(1)
	}

	err = bs.Ping(serverCtx)
	if err != nil {
		log.Error("run cannot ping s3 storage", slog.Any("error", err))
		os.Exit(1)
	}

	authRepository := repository.NewBucketAuthRepository(cfg.APISecretHash, cfg.DefaultRole)
	entryRepository := repository.NewBucketEntryRepository(bs)
	treatmentRepository := repository.NewBucketTreatmentRepository(bs)
	nightscoutRepository := repository.NewNightscoutRepository()

	err = entryRepository.Boot(serverCtx)
	if err != nil {
		log.Error("run cannot load entries", slog.Any("error", err))
	}

	err = treatmentRepository.Boot(serverCtx)
	if err != nil {
		log.Error("run cannot load treatments", slog.Any("error", err))
	}

	authService := &models.AuthService{AuthRepository: authRepository}

	cgm := repository.NewCGMLibrelinkupRepository(repository.LLUConfig{
		Region:   strings.ToLower(os.Getenv("LINK_UP_REGION")),
		Username: os.Getenv("LINK_UP_USERNAME"),
		Password: os.Getenv("LINK_UP_PASSWORD"),
	})

	apiV1C := controllers.ApiV1{
		EntryRepository:      entryRepository,
		TreatmentRepository:  treatmentRepository,
		NightscoutRepository: nightscoutRepository,
	}
	apiV1mw := controllers.ApiV1AuthnMiddleware{
		AuthService: authService,
	}

	// nb polling transport flushes (ie sends response even if there's no data)
	// every PingInterval
	sockIDGenerator := &SockIDGenerator{}
	sockSvr := socketio.NewServer(&engineio.Options{
		SessionIDGenerator: sockIDGenerator,
		PingInterval:       25 * time.Second,
		PingTimeout:        60 * time.Second,
		Transports: []transport.Transport{
			// NB websocket transport currently disabled during development,
			// as it's not playing nicely with my proxy setup
			&polling.Transport{
				//CheckOrigin: allowOriginFunc,
			},
		},
	})
	socketController := controllers.SocketController{
		Context:              ctx,
		SockSvr:              sockSvr,
		EntryRepository:      entryRepository,
		TreatmentRepository:  treatmentRepository,
		NightscoutRepository: nightscoutRepository,
	}

	sockSvr.OnConnect("/", socketController.OnConnect)
	sockSvr.OnDisconnect("/", socketController.OnDisconnect)
	sockSvr.OnError("/", socketController.OnError)
	sockSvr.OnEvent("/", "authorize", socketController.Authorize)
	sockSvr.OnEvent("/", "loadRetro", socketController.LoadRetro)
	sockSvr.OnEvent("/alarm", "", socketController.Alarm)

	go sockSvr.Serve()
	defer sockSvr.Close()

	// ingestor can trigger events
	if cgm.IsConfigured() {
		startIngestor(serverCtx, entryRepository, sockSvr, cgm)
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
		r.With(apiV1mw.Authz("api:entries:read")).Get("/entries/sgv", apiV1C.ListSGVs)
		r.With(apiV1mw.Authz("api:entries:read")).Get("/entries/current", apiV1C.LatestEntry)

		r.With(apiV1mw.Authz("api:entries:read")).Get("/treatments", apiV1C.ListTreatments)
		r.With(apiV1mw.Authz("api:entries:create")).Post("/treatments", apiV1C.CreateTreatments)
		r.With(apiV1mw.Authz("api:entries:create")).Put("/treatments", apiV1C.PutTreatment)
		r.With(apiV1mw.Authz("api:entries:read")).Get("/treatments/{oid:[a-f0-9]{24}}", apiV1C.TreatmentByOid)
		r.With(apiV1mw.Authz("api:entries:create")).Delete("/treatments/{oid:[a-f0-9]{24}}", apiV1C.DeleteTreatment)

		r.With(apiV1mw.Authz("api:entries:read")).Get("/experiments/test", apiV1C.StatusCheck)
		r.Get("/status", apiV1C.GetStatus)
		r.Get("/adminnotifies", apiV1C.GetAdminnotifies)
		r.Get("/verifyauth", apiV1C.GetVerifyauth)
	})
	r.Mount("/debug", middleware.Profiler())
	r.Mount("/socket.io", sockSvr)

	workDir, _ := os.Getwd()
	filesDir := http.Dir(filepath.Join(workDir, "dist"))
	FileServer(r, "/", filesDir)

	// TODO look at how to prevent shutdown if s3 writes are in-progress?
	server := &http.Server{Addr: cfg.Server.Address, Handler: r}

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
		log.Info("http server shutdown complete")

		err = sockSvr.Close()
		if err != nil {
			log.Error("cannot close socketio server", slog.Any("error", err))
		}
		log.Info("socketio server shutdown complete")

		serverStopCtx()
	}()

	log.Info("Starting server on", "address", cfg.Server.Address)
	err = server.ListenAndServe()
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Error("server terminated", slog.Any("error", err))
	}
	log.Info("shutting down")
	<-serverCtx.Done()
}

// FileServer conveniently sets up a http.FileServer handler to serve
// static files from a http.FileSystem.
func FileServer(r chi.Router, path string, root http.FileSystem) {
	if strings.ContainsAny(path, "{}*") {
		panic("FileServer does not permit any URL parameters.")
	}

	if path != "/" && path[len(path)-1] != '/' {
		r.Get(path, http.RedirectHandler(path+"/", 301).ServeHTTP)
		path += "/"
	}
	path += "*"

	r.Get(path, func(w http.ResponseWriter, r *http.Request) {
		rctx := chi.RouteContext(r.Context())
		pathPrefix := strings.TrimSuffix(rctx.RoutePattern(), "/*")
		fs := http.StripPrefix(pathPrefix, http.FileServer(root))
		fs.ServeHTTP(w, r)
	})
}

func startIngestor(ctx context.Context, entryRepository *repository.BucketEntryRepository, sockSvr *socketio.Server, cgm *repository.CGMLibrelinkupRepository) {
	log := slogctx.FromCtx(ctx)

	ingestOnce(ctx, entryRepository, cgm)

	go func() {
		log.Info("starting ingester")

		ticker := time.NewTicker(time.Second * 60)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				log.Debug("ingester tick")
				newEntry := ingestOnce(ctx, entryRepository, cgm)
				if newEntry != nil {
					sockSvr.BroadcastToRoom("/", "myRoom", "dataUpdate",
						map[string]interface{}{
							"delta":       true,
							"lastUpdated": newEntry.Time.UnixMilli(),
							"sgvs": []any{
								map[string]interface{}{
									"_id":       newEntry.Oid,
									"mgdl":      newEntry.SgvMgdl,
									"mills":     newEntry.Time.UnixMilli(),
									"device":    newEntry.Device,
									"direction": newEntry.Direction,
									"type":      "sgv",
								},
							},
						},
					)

				}
			case <-ctx.Done():
				return
			}
		}
	}()
}

func ingestOnce(ctx context.Context, entryRepository *repository.BucketEntryRepository, cgm *repository.CGMLibrelinkupRepository) *models.Entry {
	log := slogctx.FromCtx(ctx)

	var mostRecentEntryTime time.Time
	entry, err := entryRepository.FetchLatestSgvEntry(ctx, time.Now())
	if err == nil {
		mostRecentEntryTime = entry.Time
	}

	newEntries, err := cgm.FetchRecent(ctx, mostRecentEntryTime)
	if err != nil {
		if cgm.ErrorIsAuthnFailed(err) {
			log.Warn("librelinkup cannot authenticate, check username/password")
		} else {
			log.Warn("llu cannot fetch entries", slog.Any("error", err))
		}
		return nil
	}
	insertedEntries := entryRepository.CreateEntries(ctx, newEntries)
	if len(insertedEntries) == 0 {
		log.Info("ingester: no new entries")
		return nil
	}

	newestEntry := insertedEntries[len(insertedEntries)-1]
	log.Info("ingested entries",
		slog.Int("numEntries", len(insertedEntries)),
		slog.Time("previousNewestEntryTime", mostRecentEntryTime),
		slog.Time("newestEntryTime", newestEntry.Time),
	)
	return &newestEntry
}

type SockIDGenerator struct{}

func (g *SockIDGenerator) NewID() string {
	return ulid.Make().String()
}
