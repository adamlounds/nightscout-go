package controllers

import (
	"context"
	"fmt"
	"github.com/DmitriyVTitov/size"
	"github.com/adamlounds/nightscout-go/models"
	socketio "github.com/googollee/go-socket.io"
	slogctx "github.com/veqryn/slog-context"
	"log/slog"
	"time"
)

type SocketController struct {
	Context context.Context
	SockSvr *socketio.Server
	EntryRepository
	TreatmentRepository
	NightscoutRepository
}

//var rfc3339msLayout = "2006-01-02T15:04:05.000Z"

func (c SocketController) OnConnect(s socketio.Conn) error {
	log := slogctx.FromCtx(c.Context)
	s.SetContext("")
	log.Debug("socket connection made",
		slog.String("connId", s.ID()),
	)
	return nil
}

type loadRetroEvent struct {
	LoadedMills int64 `json:"loadedMills"`
}

func (c SocketController) LoadRetro(s socketio.Conn, msg loadRetroEvent) {
	log := slogctx.FromCtx(c.Context)

	log.Debug("loadRetro event",
		slog.Int64("loadedMills", msg.LoadedMills),
		slog.Any("conn ctx", s.Context()),
		slog.String("connId", s.ID()),
	)
	s.SetContext("conn has loaded retro")
	s.Emit("retroUpdate", map[string]any{"devicestatus": []any{}})
}

type authorizeEvent struct {
	Client       string `json:"client"`  // 'web' | 'phone' | 'pump'
	Secret       string `json:"secret"`  // hash of secret or token
	HistoryHours int64  `json:"history"` // optional, #hours of history wanted
}

func (c SocketController) Authorize(s socketio.Conn, msg authorizeEvent) any {
	ctx := c.Context
	log := slogctx.FromCtx(ctx)
	s.SetContext(fmt.Sprintf("conn has authorized [%s]", msg.Secret))
	log.Debug("socket event: authorize",
		slog.String("connId", s.ID()),
		slog.String("secret", msg.Secret),
		slog.String("client", msg.Client),
		slog.Int64("historyHours", msg.HistoryHours),
	)
	numHours := int(msg.HistoryHours)
	if numHours == 0 {
		numHours = 48
	}
	s.Emit("clients", c.SockSvr.Count())
	s.Emit("connected")
	s.Join("myRoom")

	count := int(numHours) * 60 // assuming one per minute
	entries, err := c.FetchLatestSGVs(ctx, time.Now(), count)
	if err != nil {
		log.Error("failed to fetch latest entries")
		entries = []models.Entry{}
	}

	minTime := time.Now().Add(time.Hour * time.Duration(-numHours))
	// socket API wants sgvs oldest-first...
	sgvs := []any{}
	for i := len(entries) - 1; i >= 0; i-- {
		entry := entries[i]
		if entry.Time.Before(minTime) {
			continue
		}
		sgvs = append(sgvs, map[string]interface{}{
			"_id":       entry.Oid,
			"mgdl":      entry.SgvMgdl,
			"mills":     entry.Time.UnixMilli(),
			"device":    entry.Device,
			"direction": entry.Direction,
			"type":      "sgv",
		})
	}

	upd := map[string]interface{}{
		"sgvs":         sgvs,
		"devicestatus": []any{},
		"cals":         []any{},
		"profiles": []any{
			map[string]any{
				"_id":            "67163276d0b32566a69e4a7e",
				"defaultProfile": "adam",
				"store": map[string]any{
					"adam": map[string]any{
						"dia": 3,
						"carbratio": []any{
							map[string]any{
								"time":          "00:00",
								"value":         30,
								"timeAsSeconds": 0,
							},
						},
						"carbs_hr": 20,
						"delay":    20,
						"sens": []any{
							map[string]any{
								"time":          "00:00",
								"value":         100,
								"timeAsSeconds": 0,
							},
						},
						"timezone": "UTC",
						"basal": []any{
							map[string]any{
								"time":          "00:00",
								"value":         0.1,
								"timeAsSeconds": 0,
							},
						},
						"target_low": []any{
							map[string]any{
								"time":          "00:00",
								"value":         3.4,
								"timeAsSeconds": 0,
							},
						},
						"target_high": []any{
							map[string]any{
								"time":          "00:00",
								"value":         13,
								"timeAsSeconds": 0,
							},
						},
						"units": "mmol",
					},
				},
				"startDate":  "1970-01-01T00:00:00.000Z",
				"mills":      0,
				"units":      "mmol",
				"created_at": "2024-10-21T10:52:38.046Z",
			},
		},
		"treatments": []any{
			map[string]any{
				"_id":        "676ed840d689f977f7c118d9",
				"enteredBy":  "webui",
				"eventType":  "Sensor Start",
				"created_at": "2024-12-16T16:39:00.000Z",
				"utcOffset":  0,
				"mills":      1734367140000,
				"mgdl":       157,
			},
		},
		"dbstats": map[string]any{
			"dataSize":  size.Of(c.EntryRepository),
			"indexSize": size.Of(c.TreatmentRepository),
		},
	}

	s.Emit("dataUpdate", upd)

	perms := map[string]bool{
		"read":            true,  // 'api:*:read'
		"write":           false, // api:*:create,update,delete
		"write_treatment": false, // api:treatments:create,update,delete
	}
	return perms
}
func (c SocketController) Alarm(s socketio.Conn, msg any) any {
	fmt.Printf("alarm: %v (%v) [%s]\n", msg, s.Context(), s.ID())
	s.Emit("clients", c.SockSvr.Count())
	return "woo"
}

func (c SocketController) OnError(s socketio.Conn, e error) {
	fmt.Printf("socket error: %s (%v) [%s]\n", e, s.Context(), s.ID())
	//c.SockSvr.Remove(s.ID()) // TODO investigate panic
	fmt.Println("meet error:", e)
}

func (c SocketController) OnDisconnect(s socketio.Conn, reason string) {
	// Add the Remove session id. Fixed the connection & mem leak
	//c.SockSvr.Remove(s.ID())
	fmt.Println("closed", reason)
}
