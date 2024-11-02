package controllers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/adamlounds/nightscout-go/models"
	"net/http"
)

type EventRepository interface {
	FetchEvent(ctx context.Context, id int) (*models.Event, error)
}

type ApiV1 struct {
	EventRepository
}

type APIV1EntryResponse struct {
	ID         string `json:"_id"`        // 67261314d689f977f773bc19
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

// /api/v1/entries?count=60&token=ffs-358de43470f328f3
func (a ApiV1) ListEntries(w http.ResponseWriter, r *http.Request) {
	//id, err := strconv.Atoi(chi.URLParam(r, "id"))
	ctx := r.Context()
	event, err := a.FetchEvent(ctx, 1)
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
		ID:         "abc123",
		Type:       event.Type,
		Mgdl:       event.Mgdl,
		Direction:  event.Direction,
		Device:     "dummydevice",
		Date:       event.CreatedAt.UnixMilli(),
		Mills:      event.CreatedAt.UnixMilli(),
		DateString: event.CreatedAt.Format("2006-01-02T15:04:05.999Z"),
		SysTime:    event.CreatedAt.Format("2006-01-02T15:04:05.999Z"),
		UtcOffset:  0,
	}
	err = json.NewEncoder(w).Encode(responseEvent)
	if err != nil {
		fmt.Printf("apiV1C ListEntries encoding failed: %v\n", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
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
