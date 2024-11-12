package controllers

import (
	"context"
	"errors"
	"fmt"
	"github.com/adamlounds/nightscout-go/models"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/render"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type EntryRepository interface {
	FetchEntryByOid(ctx context.Context, oid string) (*models.Entry, error)
	FetchLatestEntry(ctx context.Context) (*models.Entry, error)
	FetchLatestEntries(ctx context.Context, maxEntries int) ([]models.Entry, error)
	CreateEntries(ctx context.Context, entries []models.Entry) ([]models.Entry, error)
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

func (a ApiV1) EntryByOid(w http.ResponseWriter, r *http.Request) {
	oid := chi.URLParam(r, "oid")
	ctx := r.Context()
	entry, err := a.FetchEntryByOid(ctx, oid)
	if err != nil {
		if errors.Is(err, models.ErrNotFound) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		fmt.Printf("entryService.ByID failed: %v\n", err)
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
		DateString: entry.CreatedTime.Format("2006-01-02T15:04:05.000Z"),
		SysTime:    entry.CreatedTime.Format("2006-01-02T15:04:05.000Z"),
		UtcOffset:  0,
	}
	render.JSON(w, r, responseEntry)
}

// LatestEntry handler supports /api/v1/entries/current endpoint: return latest sgv entry
func (a ApiV1) LatestEntry(w http.ResponseWriter, r *http.Request) {

	ctx := r.Context()
	entry, err := a.FetchLatestEntry(ctx)
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
		Date:       entry.CreatedTime.UnixMilli(),
		Mills:      entry.CreatedTime.UnixMilli(),
		DateString: entry.CreatedTime.Format("2006-01-02T15:04:05.000Z"),
		SysTime:    entry.CreatedTime.Format("2006-01-02T15:04:05.000Z"),
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
	mbg entryTypeID = 0
	sgv entryTypeID = 1
	cal entryTypeID = 2
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
	entries, err := a.FetchLatestEntries(ctx, count)
	if err != nil {
		fmt.Printf("entryService.ByID failed: %v\n", err)
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
			http.Error(w, "invalid date format", http.StatusBadRequest)
			return
		}

		entries = append(entries, models.Entry{
			Type:        "sgv",
			SgvMgdl:     reqEntry.SgvMgdl,
			Direction:   reqEntry.Direction,
			Time:        entryTime,
			Device:      reqEntry.Device,
			CreatedTime: now,
		})
	}

	insertedEntries, err := a.EntryRepository.CreateEntries(ctx, entries)
	if err != nil {
		fmt.Printf("could not create entries: %v\n", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	// NB while swagger.json says this should return the _rejected_ entries,
	// cgm_remote_monitor returns the accepted entries
	a.renderEntryList(w, r, insertedEntries)
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
				DateString: entry.Time.Format("2006-01-02T15:04:05.000Z"),
				SysTime:    entry.Time.Format("2006-01-02T15:04:05.000Z"),
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
			fmt.Sprintf(`"%s"`, entry.Time.Format("2006-01-02T15:04:05.000Z")),
			strconv.FormatInt(entry.Time.UnixMilli(), 10),
			strconv.Itoa(entry.SgvMgdl),
			direction,
			fmt.Sprintf(`"%s"`, entry.Device),
		}
		responseEntries = append(responseEntries, strings.Join(parts, "\t"))
	}

	render.PlainText(w, r, strings.Join(responseEntries, "\n"))
}
