package controllers

import (
	"context"
	"errors"
	"fmt"
	"github.com/adamlounds/nightscout-go/models"
	nightscoutstore "github.com/adamlounds/nightscout-go/stores/nightscout"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/render"
	slogctx "github.com/veqryn/slog-context"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type EntryRepository interface {
	FetchEntryByOid(ctx context.Context, oid string) (*models.Entry, error)
	FetchLatestSgvEntry(ctx context.Context, maxTime time.Time) (*models.Entry, error)
	FetchLatestEntries(ctx context.Context, maxTime time.Time, maxEntries int) ([]models.Entry, error)
	CreateEntries(ctx context.Context, entries []models.Entry) []models.Entry
}
type AuthRepository interface {
	GetAPISecretHash(ctx context.Context) string
	GetDefaultRole(ctx context.Context) string
	// something about fetching roles too, hence auth not authn in repository name
}

type ApiV1 struct {
	EntryRepository
}

type APIV1EntryResponse struct {
	Oid        string `json:"_id"`        // mongo object id [0-9a-f]{24} eg "67261314d689f977f773bc19"
	Type       string `json:"type"`       // "sgv"
	Direction  string `json:"direction"`  // "Flat"
	Device     string `json:"device"`     // "nightscout-librelink-up"
	DateString string `json:"dateString"` // rfc3339 plus ms
	SysTime    string `json:"sysTime"`    // same as dateString
	Date       int64  `json:"date"`       // ms since epoch
	Mills      int64  `json:"mills"`      // ms since epoch
	UtcOffset  int64  `json:"utcOffset"`  // always 0
	SgvMgdl    int    `json:"sgv"`        //
}

type APIV1EntryRequest struct {
	Type      string `json:"type"`
	Direction string `json:"direction"`
	Device    string `json:"device"`
	Date      string `json:"dateString"`
	SgvMgdl   int    `json:"sgv"`
}

var rfc3339msLayout = "2006-01-02T15:04:05.000Z"

func (a ApiV1) EntryByOid(w http.ResponseWriter, r *http.Request) {
	oid := chi.URLParam(r, "oid")
	ctx := r.Context()
	log := slogctx.FromCtx(ctx)
	entry, err := a.FetchEntryByOid(ctx, oid)
	if err != nil {
		if errors.Is(err, models.ErrNotFound) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		log.Warn("entryService.ByID failed", slog.Any("error", err))
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	responseEntry := &APIV1EntryResponse{
		Oid:        entry.Oid,
		Type:       entry.Type,
		SgvMgdl:    entry.SgvMgdl,
		Direction:  entry.Direction,
		Device:     entry.Device,
		Date:       entry.CreatedTime.UnixMilli(),
		Mills:      entry.CreatedTime.UnixMilli(),
		DateString: entry.CreatedTime.Format(rfc3339msLayout),
		SysTime:    entry.CreatedTime.Format(rfc3339msLayout),
		UtcOffset:  0,
	}
	render.JSON(w, r, responseEntry)
}

// LatestEntry handler supports /api/v1/entries/current endpoint: return latest sgv entry
func (a ApiV1) LatestEntry(w http.ResponseWriter, r *http.Request) {

	ctx := r.Context()
	entry, err := a.FetchLatestSgvEntry(ctx, time.Now())
	if err != nil {
		if errors.Is(err, models.ErrNotFound) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	responseEntry := &APIV1EntryResponse{
		Oid:        entry.Oid,
		Type:       entry.Type,
		SgvMgdl:    entry.SgvMgdl,
		Direction:  entry.Direction,
		Device:     entry.Device,
		Date:       entry.Time.UnixMilli(),
		Mills:      entry.Time.UnixMilli(),
		DateString: entry.Time.Format(rfc3339msLayout),
		SysTime:    entry.CreatedTime.Format(rfc3339msLayout),
		UtcOffset:  0,
	}
	render.JSON(w, r, responseEntry)
}

func (a ApiV1) urlFormat(r *http.Request) string {
	ctx := r.Context()
	urlFormat, _ := ctx.Value(middleware.URLFormatCtxKey).(string)
	if urlFormat != "" {
		return urlFormat
	}
	ct := r.Header.Get("content-type")
	if ct == "application/json" {
		return "json"
	}

	return ""
}

type directionID uint8

const (
	None           directionID = 0
	DoubleUp       directionID = 1
	SingleUp       directionID = 2
	FortyFiveUp    directionID = 3
	Flat           directionID = 4
	FortyFiveDown  directionID = 5
	SingleDown     directionID = 6
	DoubleDown     directionID = 7
	NotComputable  directionID = 8
	RateOutOfRange directionID = 9
)

var directionIDByName = map[string]directionID{
	"NONE":           None,
	"DoubleUp":       DoubleUp,
	"SingleUp":       SingleUp,
	"FortyFiveUp":    FortyFiveUp,
	"Flat":           Flat,
	"FortyFiveDown":  FortyFiveDown,
	"SingleDown":     SingleDown,
	"DoubleDown":     DoubleDown,
	"NotComputable":  NotComputable,
	"RateOutOfRange": RateOutOfRange,
}

type entryTypeID uint8

const (
	mbg entryTypeID = 1
	sgv entryTypeID = 2
	cal entryTypeID = 3
)

var entryTypeIDByName = map[string]entryTypeID{
	"mbg": mbg,
	"sgv": sgv,
	"cal": cal,
}

// ListEntries returns zero or more entries matching any conditions in the query
// Default is `count=10`, for only 10 latest entries, reverse sorted by date
// /api/v1/entries?count=60&token=ffs-358de43470f328f3
// /api/v1/entries?count=1 for FreeStyle LibreLink Up NightScout Uploader
func (a ApiV1) ListEntries(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	log := slogctx.FromCtx(ctx)

	count, err := strconv.Atoi(r.URL.Query().Get("count"))
	if err != nil {
		if r.URL.Query().Get("count") != "" {
			http.Error(w, "count must be an integer", http.StatusBadRequest)
			return
		}
		count = 20
	}
	if count < 1 {
		http.Error(w, "count must be >= 1", http.StatusBadRequest)
		return
	}
	if count > 50000 {
		http.Error(w, "count must be <= 50000", http.StatusBadRequest)
		return
	}
	entries, err := a.FetchLatestEntries(ctx, time.Now(), count)
	if err != nil {
		log.Warn("entryService.ByID failed", slog.Any("error", err))
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	a.renderEntryList(w, r, entries)

}

// receive
// [  {
//    "type": "sgv",
//    "sgv": 158,
//    "direction": "Flat",
//    "device": "nightscout-librelink-up",
//    "date": 1730549212000,
//    "dateString": "2024-11-02T12:06:52.000Z"
//  }, ... ]

// CreateEntries allows creation of new entries via POST /api/v1/entries
func (a ApiV1) CreateEntries(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	log := slogctx.FromCtx(ctx)

	var requestEntries []APIV1EntryRequest
	if err := render.DecodeJSON(r.Body, &requestEntries); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	var entries []models.Entry
	for _, reqEntry := range requestEntries {
		now := time.Now()
		entryTime, err := time.Parse(time.RFC3339, reqEntry.Date)
		if err != nil {
			// also try offset without a colon, as used by xDrip back-fill
			entryTime, err = time.Parse("2006-01-02T15:04:05.999999999Z0700", reqEntry.Date)
			if err != nil {
				log.Info("invalid date format", slog.String("entryDate", reqEntry.Date))
				http.Error(w, "invalid date format", http.StatusBadRequest)
				return
			}
			reqEntry.Date = entryTime.Format(rfc3339msLayout)
		}
		_, ok := entryTypeIDByName[reqEntry.Type]
		if !ok {
			log.Info("unknown type", slog.String("type", reqEntry.Type))
			http.Error(w, "invalid type", http.StatusBadRequest)
			return
		}

		entries = append(entries, models.Entry{
			Type:        reqEntry.Type,
			SgvMgdl:     reqEntry.SgvMgdl,
			Direction:   reqEntry.Direction,
			Time:        entryTime,
			Device:      reqEntry.Device,
			CreatedTime: now,
		})
	}

	insertedEntries := a.EntryRepository.CreateEntries(ctx, entries)

	// NB while swagger.json says this should return the _rejected_ entries,
	// cgm_remote_monitor returns the accepted entries
	a.renderEntryList(w, r, insertedEntries)
}

type ImportNSRequest struct {
	Url       string `json:"url"`
	Token     string `json:"token"`
	APISecret string `json:"api_secret"`
}

func (a ApiV1) ImportNightscoutEntries(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	log := slogctx.FromCtx(ctx)

	// TODO look at https://grafana.com/blog/2024/02/09/how-i-write-http-services-in-go-after-13-years/#validating-data
	// pattern for validation
	var req ImportNSRequest
	if err := render.DecodeJSON(r.Body, &req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.Url == "" {
		log.Debug("missing url")
		http.Error(w, "missing url", http.StatusBadRequest)
		return
	}

	nsUrl, err := url.Parse(req.Url)
	if err != nil {
		// Pretty rare, Parse is very lax. ":" seems to work :)
		log.Debug("bad url: parse fail", slog.String("url", req.Url))
		http.Error(w, "bad url", http.StatusBadRequest)
		return
	}
	if nsUrl.Scheme != "http" && nsUrl.Scheme != "https" {
		log.Debug("bad url: unsupported scheme", slog.String("url", req.Url))
		http.Error(w, "url must be http/https", http.StatusBadRequest)
		return
	}
	if nsUrl.Host == "" {
		log.Debug("bad url: no host", slog.String("url", req.Url))
		http.Error(w, "url must include a hostname", http.StatusBadRequest)
		return
	}

	if req.Token == "" && req.APISecret == "" {
		log.Debug("missing credentials", slog.String("token", req.Token), slog.String("api_secret", req.APISecret))
		http.Error(w, "token or api_secret must be supplied", http.StatusBadRequest)
		return
	}
	if req.APISecret != "" && len(req.APISecret) < 12 {
		log.Debug("credentials: api_secret too short", slog.String("api_secret", req.APISecret))
		http.Error(w, "api_secret must be at least 12 characters long", http.StatusBadRequest)
		return
	}

	// name-<16 hexits>
	if req.Token != "" && len(req.Token) < 18 {
		log.Debug("credentials: token too short", slog.String("api_secret", req.APISecret))
		http.Error(w, "token must be at least 18 characters long", http.StatusBadRequest)
		return
	}

	u := &url.URL{Scheme: nsUrl.Scheme, Host: nsUrl.Host}

	nsCfg := nightscoutstore.NightscoutConfig{
		URL:       u,
		Token:     req.Token,
		APISecret: req.APISecret,
	}

	store := nightscoutstore.New(nsCfg)

	entries, err := store.FetchAllEntries(ctx)
	fmt.Printf("received entries %v\n", entries)

	w.WriteHeader(http.StatusCreated)
}

func (a ApiV1) renderEntryList(w http.ResponseWriter, r *http.Request, entries []models.Entry) {
	urlFormat := a.urlFormat(r)

	if urlFormat == "json" {

		var response []APIV1EntryResponse
		for _, entry := range entries {
			response = append(response, APIV1EntryResponse{
				Oid:        entry.Oid,
				Type:       entry.Type,
				SgvMgdl:    entry.SgvMgdl,
				Direction:  entry.Direction,
				Device:     entry.Device,
				Date:       entry.Time.UnixMilli(),
				Mills:      entry.Time.UnixMilli(),
				DateString: entry.Time.Format(rfc3339msLayout),
				SysTime:    entry.Time.Format(rfc3339msLayout),
				UtcOffset:  0,
			})
		}

		render.JSON(w, r, response)
		return
	}

	if urlFormat != "" {
		http.Error(w, "unsupported media type", http.StatusUnsupportedMediaType)
		return
	}

	var responseEntries []string
	for _, entry := range entries {
		direction := ""
		if entry.Direction != "" {
			direction = fmt.Sprintf(`"%s"`, entry.Direction)
		}
		parts := []string{
			fmt.Sprintf(`"%s"`, entry.Time.Format(rfc3339msLayout)),
			strconv.FormatInt(entry.Time.UnixMilli(), 10),
			strconv.Itoa(entry.SgvMgdl),
			direction,
			fmt.Sprintf(`"%s"`, entry.Device),
		}
		responseEntries = append(responseEntries, strings.Join(parts, "\t"))
	}

	render.PlainText(w, r, strings.Join(responseEntries, "\n"))
}
