package controllers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/adamlounds/nightscout-go/models"
	"github.com/go-chi/chi/v5"
	"net/http"
	"strconv"
)

type EventRepository interface {
	FetchEventByOid(ctx context.Context, oid string) (*models.Event, error)
	FetchLatestEvent(ctx context.Context) (*models.Event, error)
	FetchLatestEvents(ctx context.Context, maxEvents int) ([]models.Event, error)
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
		DateString: event.CreatedTime.Format("2006-01-02T15:04:05.999Z"),
		SysTime:    event.CreatedTime.Format("2006-01-02T15:04:05.999Z"),
		UtcOffset:  0,
	}
	json.NewEncoder(w).Encode(responseEvent)
}

// /api/v1/entries/current - return latest sgv entry
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
		DateString: event.CreatedTime.Format("2006-01-02T15:04:05.999Z"),
		SysTime:    event.CreatedTime.Format("2006-01-02T15:04:05.999Z"),
		UtcOffset:  0,
	}
	json.NewEncoder(w).Encode(responseEvent)
}

// Default is `count=10`, for only 10 latest entries, reverse sorted by date
// /api/v1/entries?count=60&token=ffs-358de43470f328f3
// /api/v1/entries?count=1 for FreeStyle LibreLink Up NightScout Uploader
func (a ApiV1) ListEntries(w http.ResponseWriter, r *http.Request) {
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
	ctx := r.Context()
	events, err := a.FetchLatestEvents(ctx, count)
	if err != nil {
		fmt.Printf("eventService.ByID failed: %v\n", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

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
			DateString: event.CreatedTime.Format("2006-01-02T15:04:05.999Z"),
			SysTime:    event.CreatedTime.Format("2006-01-02T15:04:05.999Z"),
			UtcOffset:  0,
		})
	}

	json.NewEncoder(w).Encode(response)
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
