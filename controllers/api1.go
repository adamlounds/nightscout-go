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
)

type EventRepository interface {
	FetchEventByOid(ctx context.Context, oid string) (*models.Event, error)
	FetchLatestEvent(ctx context.Context) (*models.Event, error)
	FetchLatestEvents(ctx context.Context, maxEvents int) ([]models.Event, error)
}
type AuthRepository interface {
	GetAPISecretHash(ctx context.Context) string
	GetDefaultRole(ctx context.Context) string
	// something about fetching roles too, hence auth not authn in repository name
}

type ApiV1 struct {
	EventRepository
}

type APIV1EntryResponse struct {
	Oid        string `json:"_id"`        // mongo object id [0-9a-f]{24} eg "67261314d689f977f773bc19"
	Type       string `json:"type"`       // "sgv"
	Mgdl       int    `json:"sgv"`        //
	Direction  string `json:"direction"`  // "Flat"
	Device     string `json:"device"`     // "nightscout-librelink-up"
	Date       int64  `json:"date"`       // ms since epoch
	DateString string `json:"dateString"` // rfc3339 plus ms
	UtcOffset  int64  `json:"utcOffset"`  // always 0
	SysTime    string `json:"sysTime"`    // same as dateString
	Mills      int64  `json:"mills"`      // ms since epoch
}

func (a ApiV1) EntryByOid(w http.ResponseWriter, r *http.Request) {
	oid := chi.URLParam(r, "oid")
	ctx := r.Context()
	event, err := a.FetchEventByOid(ctx, oid)
	if err != nil {
		if errors.Is(err, models.ErrNotFound) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		fmt.Printf("eventService.ByID failed: %v\n", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	responseEvent := &APIV1EntryResponse{
		Oid:        event.Oid,
		Type:       event.Type,
		Mgdl:       event.Mgdl,
		Direction:  event.Direction,
		Device:     "dummydevice",
		Date:       event.CreatedTime.UnixMilli(),
		Mills:      event.CreatedTime.UnixMilli(),
		DateString: event.CreatedTime.Format("2006-01-02T15:04:05.000Z"),
		SysTime:    event.CreatedTime.Format("2006-01-02T15:04:05.000Z"),
		UtcOffset:  0,
	}
	render.JSON(w, r, responseEvent)
}

// LatestEntry handler supports /api/v1/entries/current endpoint: return latest sgv entry
func (a ApiV1) LatestEntry(w http.ResponseWriter, r *http.Request) {

	ctx := r.Context()
	event, err := a.FetchLatestEvent(ctx)
	if err != nil {
		if errors.Is(err, models.ErrNotFound) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	responseEvent := &APIV1EntryResponse{
		Oid:        event.Oid,
		Type:       event.Type,
		Mgdl:       event.Mgdl,
		Direction:  event.Direction,
		Device:     "dummydevice",
		Date:       event.CreatedTime.UnixMilli(),
		Mills:      event.CreatedTime.UnixMilli(),
		DateString: event.CreatedTime.Format("2006-01-02T15:04:05.000Z"),
		SysTime:    event.CreatedTime.Format("2006-01-02T15:04:05.000Z"),
		UtcOffset:  0,
	}
	render.JSON(w, r, responseEvent)
}

// Default is `count=10`, for only 10 latest entries, reverse sorted by date
// /api/v1/entries?count=60&token=ffs-358de43470f328f3
// /api/v1/entries?count=1 for FreeStyle LibreLink Up NightScout Uploader
func (a ApiV1) ListEntries(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	urlFormat, _ := ctx.Value(middleware.URLFormatCtxKey).(string)

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
	events, err := a.FetchLatestEvents(ctx, count)
	if err != nil {
		fmt.Printf("eventService.ByID failed: %v\n", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	if urlFormat == "json" {

		var response []APIV1EntryResponse
		for _, event := range events {
			response = append(response, APIV1EntryResponse{
				Oid:        event.Oid,
				Type:       event.Type,
				Mgdl:       event.Mgdl,
				Direction:  event.Direction,
				Device:     "dummydevice",
				Date:       event.CreatedTime.UnixMilli(),
				Mills:      event.CreatedTime.UnixMilli(),
				DateString: event.CreatedTime.Format("2006-01-02T15:04:05.000Z"),
				SysTime:    event.CreatedTime.Format("2006-01-02T15:04:05.000Z"),
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
	for _, event := range events {
		parts := []string{
			event.CreatedTime.Format("2006-01-02T15:04:05.000Z"),
			strconv.FormatInt(event.CreatedTime.UnixMilli(), 10),
			strconv.Itoa(event.Mgdl),
			event.Direction,
			"dummydevice",
		}
		responseEntries = append(responseEntries, strings.Join(parts, "\t"))
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	render.PlainText(w, r, strings.Join(responseEntries, "\n"))

}

// receive
// [
//  {
//    type: 'sgv',
//    sgv: 158,
//    direction: 'Flat',
//    device: 'nightscout-librelink-up',
//    date: 1730549212000,
//    dateString: '2024-11-02T12:06:52.000Z'
//  }
//]
//type T struct {
//	Type       string    `json:"type"`
//	Sgv        int       `json:"sgv"`
//	Direction  string    `json:"direction"`
//	Device     string    `json:"device"`
//	Date       int64     `json:"date"`
//	DateString time.Time `json:"dateString"`
//}
