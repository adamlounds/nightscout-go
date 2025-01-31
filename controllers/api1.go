package controllers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	repository "github.com/adamlounds/nightscout-go/adapters"
	"github.com/adamlounds/nightscout-go/middleware"
	"github.com/adamlounds/nightscout-go/models"
	"github.com/go-chi/chi/v5"
	chimiddleware "github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/render"
	slogctx "github.com/veqryn/slog-context"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

var ErrInvalidTimeString = errors.New("invalid time string")

type EntryRepository interface {
	FetchEntryByOid(ctx context.Context, oid string) (*models.Entry, error)
	FetchLatestSgvEntry(ctx context.Context, maxTime time.Time) (*models.Entry, error)
	FetchLatestEntries(ctx context.Context, maxTime time.Time, maxEntries int) ([]models.Entry, error)
	FetchLatestSGVs(ctx context.Context, maxTime time.Time, maxEntries int) ([]models.Entry, error)
	CreateEntries(ctx context.Context, entries []models.Entry) []models.Entry
}
type TreatmentRepository interface {
	Boot(ctx context.Context) error
	FetchTreatmentByOid(ctx context.Context, oid string) (*models.Treatment, error)
	DeleteTreatmentByOid(ctx context.Context, oid string) error
	FetchLatestTreatments(ctx context.Context, maxTime time.Time, maxTreatments int) ([]models.Treatment, error)
	CreateTreatments(ctx context.Context, treatments []models.Treatment) []models.Treatment
	UpdateTreatmentByOid(ctx context.Context, oid string, treatment *models.Treatment) error
}
type AuthRepository interface {
	GetAPISecretHash(ctx context.Context) string
	GetDefaultRole(ctx context.Context) string
	// something about fetching roles too, hence auth not authn in repository name
}

type NightscoutRepository interface {
	FetchAllEntries(ctx context.Context, nsCfg repository.NightscoutConfig) ([]models.Entry, error)
}

type ApiV1 struct {
	EntryRepository
	TreatmentRepository
	NightscoutRepository
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

type APIV1TreatmentResponse struct {
	Oid        string `json:"_id"`        // mongo object id [0-9a-f]{24} eg "67261314d689f977f773bc19"
	Type       string `json:"type"`       // "sgv"
	DateString string `json:"created_at"` // rfc3339 plus ms
	Mills      int64  `json:"mills"`      // ms since epoch

	// optional for all treatments
	Carbs     *float64 `json:"carbs"`   // null when not specified
	Insulin   *float64 `json:"insulin"` // null when not specified
	EnteredBy string   `json:"enteredBy,omitempty"`
	Notes     string   `json:"notes,omitempty"`
}

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

	a.renderEntryList(w, r, []models.Entry{*entry})
}

func (a ApiV1) urlFormat(r *http.Request) string {
	ctx := r.Context()
	urlFormat, _ := ctx.Value(chimiddleware.URLFormatCtxKey).(string)
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

func (a ApiV1) ListSGVs(w http.ResponseWriter, r *http.Request) {
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
	entries, err := a.FetchLatestSGVs(ctx, time.Now(), count)
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
		entryTime, err := parseTime(reqEntry.Date)
		if err != nil || entryTime.IsZero() {
			log.Info("invalid date format", slog.String("entryDate", reqEntry.Date))
			http.Error(w, "invalid date format", http.StatusBadRequest)
			return
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
	if req.Token != "" && len(req.Token) < 17 {
		log.Debug("credentials: token too short", slog.String("api_secret", req.APISecret))
		http.Error(w, "token must be at least 17 characters long", http.StatusBadRequest)
		return
	}

	u := &url.URL{Scheme: nsUrl.Scheme, Host: nsUrl.Host}

	nsCfg := repository.NightscoutConfig{
		URL:       u,
		Token:     req.Token,
		APISecret: req.APISecret,
	}

	entries, err := a.FetchAllEntries(ctx, nsCfg)
	if err != nil {
		log.Info("cannot fetch entries from ns", slog.Any("err", err))
		http.Error(w, "Cannot fetch entries from remote nightscout instance", http.StatusBadRequest)
		return
	}
	log.Debug("fetched entries from remote nightscout instance",
		slog.Int("numEntries", len(entries)),
		slog.Time("latestEntry", entries[0].Time),
		slog.Time("earliestEntry", entries[len(entries)-1].Time),
	)

	// We receive entries from ns in most-recent-first order. Pass them to
	// CreateEntries in ascending date order for slight speedup
	for i, j := 0, len(entries)-1; i < j; i, j = i+1, j-1 {
		entries[i], entries[j] = entries[j], entries[i]
	}

	insertedEntries := a.EntryRepository.CreateEntries(ctx, entries)
	log.Info("imported entries from remote nightscout instance",
		slog.Int("numEntries", len(insertedEntries)),
		slog.Time("latestEntry", insertedEntries[0].Time),
		slog.Time("earliestEntry", insertedEntries[len(insertedEntries)-1].Time),
	)

	w.WriteHeader(http.StatusOK)
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

	render.PlainText(w, r, strings.Join(responseEntries, "\r\n"))
}

func (a ApiV1) renderTreatmentList(w http.ResponseWriter, r *http.Request, treatments []models.Treatment) {
	// treatments are always json, there are too many distinct fields for tsv

	response := make([]map[string]interface{}, 0)
	for _, treatment := range treatments {
		tTime := treatment.Time
		var treatmentData = map[string]interface{}{
			"_id":        treatment.ID,
			"eventType":  treatment.Type,
			"mills":      tTime.UnixMilli(),
			"created_at": tTime.Format(rfc3339msLayout),
		}
		for k, v := range treatment.Fields {
			treatmentData[k] = v
		}

		response = append(response, treatmentData)
	}

	render.JSON(w, r, response)
}

func (a ApiV1) StatusCheck(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"ok"}`))
}

func (a ApiV1) GetStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)

	// spike: hardcoded response
	w.Write([]byte(`{"status":"ok","name":"nightscout","version":"16.0.0","serverTime":"2024-12-30T13:14:22.499Z","serverTimeEpoch":1735564462499,"apiEnabled":true,"careportalEnabled":true,"boluscalcEnabled":true,"settings":{"units":"mmol","timeFormat":12,"dayStart":7,"dayEnd":21,"nightMode":false,"editMode":true,"showRawbg":"never","customTitle":"Nightscout","theme":"default","alarmUrgentHigh":true,"alarmUrgentHighMins":[30,60,90,120],"alarmHigh":true,"alarmHighMins":[30,60,90,120],"alarmLow":true,"alarmLowMins":[15,30,45,60],"alarmUrgentLow":true,"alarmUrgentLowMins":[15,30,45],"alarmUrgentMins":[30,60,90,120],"alarmWarnMins":[30,60,90,120],"alarmTimeagoWarn":true,"alarmTimeagoWarnMins":15,"alarmTimeagoUrgent":true,"alarmTimeagoUrgentMins":30,"alarmPumpBatteryLow":false,"language":"en","scaleY":"log","showPlugins":"careportal openaps pump iob sage cage delta direction upbat","showForecast":"openaps","focusHours":3,"heartbeat":60,"baseURL":"","authDefaultRoles":"readable devicestatus-upload","thresholds":{"bgHigh":144,"bgTargetTop":126,"bgTargetBottom":70,"bgLow":69},"insecureUseHttp":false,"secureHstsHeader":true,"secureHstsHeaderIncludeSubdomains":false,"secureHstsHeaderPreload":false,"secureCsp":false,"deNormalizeDates":false,"showClockDelta":false,"showClockLastTime":false,"frameUrl1":"","frameUrl2":"","frameUrl3":"","frameUrl4":"","frameUrl5":"","frameUrl6":"","frameUrl7":"","frameUrl8":"","frameName1":"","frameName2":"","frameName3":"","frameName4":"","frameName5":"","frameName6":"","frameName7":"","frameName8":"","authFailDelay":5000,"adminNotifiesEnabled":true,"DEFAULT_FEATURES":["bgnow","delta","direction","timeago","devicestatus","upbat","errorcodes","profile","bolus","dbsize","runtimestate","basal","careportal"],"alarmTypes":["simple"],"enable":["careportal","boluscalc","food","bwp","cage","sage","iage","iob","cob","basal","ar2","rawbg","pushover","bgi","pump","openaps","cors","treatmentnotify","bgnow","delta","direction","timeago","devicestatus","upbat","errorcodes","profile","bolus","dbsize","runtimestate","simplealarms"]},"extendedSettings":{"devicestatus":{"advanced":true,"days":1}},"authorized":null,"runtimeState":"loaded"}`))
}
func (a ApiV1) GetAdminnotifies(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)

	// spike: hardcoded response
	w.Write([]byte(`{"status":200,"message":{"notifies":[],"notifyCount":0}}`))
}
func (a ApiV1) GetVerifyauth(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	log := slogctx.FromCtx(ctx)
	authn := middleware.GetAuthn(ctx)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)

	if authn == nil {
		log.Debug("verifyauth: not authenticated")
		http.Error(w, "not authenticated", http.StatusUnauthorized)
		w.Write([]byte(`{"status":200,"message":{"canRead":true,"canWrite":false,"isAdmin":false,"message":"UNAUTHORIZED","rolefound":"NOTFOUND","permissions":"DEFAULT"}}`))
		return
	}
	canRead := authn.IsPermitted(ctx, "*:*:read")
	canWrite := authn.IsPermitted(ctx, "*:*:write")
	isAdmin := authn.IsPermitted(ctx, "*")
	isAuthorized := canRead && !authn.AuthSubject.IsAnonymous()

	message := "UNAUTHORIZED"
	if isAuthorized {
		message = "OK"
	}

	permissions := "DEFAULT"
	if !authn.AuthSubject.IsAnonymous() {
		permissions = "ROLE"
	}

	// mirroring nightscout's observed behaviour: admin is NOTFOUND
	rolefound := "FOUND"
	if authn.AuthSubject.IsAnonymous() || authn.AuthSubject.Name == "admin" {
		rolefound = "NOTFOUND"
	}

	response := map[string]interface{}{
		"status": 200,
		"message": map[string]interface{}{
			"canRead":     canRead,
			"canWrite":    canWrite,
			"isAdmin":     isAdmin,
			"message":     message,
			"rolefound":   rolefound,
			"permissions": permissions,
		},
	}

	render.JSON(w, r, response)
}

func (a ApiV1) ListTreatments(w http.ResponseWriter, r *http.Request) {
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

	treatments, err := a.FetchLatestTreatments(ctx, time.Now(), count)
	if err != nil {
		log.Warn("FetchLatestTreatments failed", slog.Any("error", err))
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	a.renderTreatmentList(w, r, treatments)
}

// api can be either json or x-www-urlencoded. ns web ui uses form,
// shuggah/xdrip uses json

// [{"enteredBy": "xDrip4iOS","eventTime": "2024-12-16T14:56:10.000Z","eventType": "Carbs","carbs": 62}]

// eventTime sent iff it's not "now". js Date.New() accepts this date format 😭
// enteredBy=adam&eventType=Carb+Correction&glucoseType=Finger&carbs=10.6&units=mg%2Fdl&eventTime=Mon+Dec+16+2024+15%3A59%3A00+GMT%2B0000+(Greenwich+Mean+Time)&created_at=2024-12-16T15%3A59%3A00.000Z
// could be `Mon Dec 16 2024 16:25:52 GMT+0000 (heure moyenne de Greenwich)` from a french browser.
type APIV1TreatmentRequest struct {
	Type           string  `json:"eventType"`
	TimeString     string  `json:"eventTime"`
	AbsorptionTime int     `json:"absorptionTime,omitempty"` // minutes
	EnteredBy      string  `json:"enteredBy,omitempty"`
	Insulin        float64 `json:"insulin,omitempty"`
	UtcOffset      int     `json:"utcOffset,omitempty"`
	Carbs          float64 `json:"carbs,omitempty"`
	Glucose        float64 `json:"glucose,omitempty"` // sgv
	GlucoseType    string  `json:"glucoseType,omitempty"`
	Units          string  `json:"units,omitempty"`
	Duration       int     `json:"duration,omitempty"`
	SensorCode     string  `json:"sensorCode,omitempty"`
	Notes          string  `json:"notes,omitempty"`
	Fat            string  `json:"fat,omitempty"`
	Protein        string  `json:"protein,omitempty"`
	PreBolus       int     `json:"preBolus,omitempty"`
	Enteredinsulin string  `json:"enteredinsulin,omitempty"`
	Relative       int     `json:"relative,omitempty"`
	SplitExt       string  `json:"splitExt,omitempty"`
	SplitNow       string  `json:"splitNow,omitempty"`
	IsAnnouncement bool    `json:"isAnnouncement,omitempty"`
}

func (a ApiV1) CreateTreatments(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	log := slogctx.FromCtx(ctx)

	body, _ := io.ReadAll(r.Body)
	defer r.Body.Close()
	//fmt.Printf("%v\n", string(body))
	var whatevs []map[string]interface{}
	err := json.Unmarshal(body, &whatevs)
	if err != nil {
		log.Info("cannot unmarshal request body", slog.Any("err", err))
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	var treatments []models.Treatment
	for _, reqTreatment := range whatevs {

		treatment, err := treatmentFromJSON(ctx, reqTreatment)
		if err != nil {
			if errors.Is(err, ErrInvalidTimeString) {
				log.Info("cannot parse treatment eventTime",
					slog.Any("eventTime", reqTreatment["eventTime"]),
					slog.Any("fields", reqTreatment),
				)
				http.Error(w, "unparseable treatment eventTime", http.StatusBadRequest)
				return
			}

			if errors.Is(err, models.ErrUnknownTreatmentType) {
				log.Info("unknown treatment type",
					slog.Any("entryType", reqTreatment["eventType"]),
					slog.Any("fields", reqTreatment),
				)
				http.Error(w, "unknown treatment type", http.StatusBadRequest)
				return
			}

			log.Info("invalid treatment",
				slog.Any("entryType", reqTreatment["eventType"]),
				slog.Any("err", err),
				slog.Any("fields", reqTreatment),
			)
			http.Error(w, "invalid treatment type", http.StatusBadRequest)
			return
		}
		treatments = append(treatments, *treatment)
	}
	log.Info("parsed treatments ok", slog.Any("treatments", treatments))

	insertedTreatments := a.TreatmentRepository.CreateTreatments(ctx, treatments)

	// NB while swagger.json says this should return the _rejected_ entries,
	// cgm_remote_monitor returns the accepted entries
	a.renderTreatmentList(w, r, insertedTreatments)
}

func treatmentFromJSON(ctx context.Context, request map[string]interface{}) (*models.Treatment, error) {
	eventType, ok := request["eventType"].(string)
	if !ok {
		return nil, errors.New("missing eventType")
	}

	eventTimeStr, ok := request["eventTime"].(string)
	if !ok {
		// cgm-remote-monitor also supports passing `created_at` since a695a1d
		createdAtStr, ok := request["created_at"].(string)
		if ok {
			eventTimeStr = createdAtStr
		} else {
			// fallback when both eventTime and created_at are null/omitted
			eventTimeStr = time.Now().UTC().Format(rfc3339msLayout)
		}
	}

	eventTime, err := parseTime(eventTimeStr)
	if err != nil {
		return nil, err
	}

	t := &models.Treatment{
		Time:   eventTime,
		Type:   eventType,
		Fields: map[string]interface{}{},
	}
	existingID, ok := request["_id"].(string)
	if ok && existingID != "" {
		t.ID = existingID
	}

	// copy non-global fields in. We need the original in case we want to log
	for k, v := range request {
		t.Fields[k] = v
	}
	delete(t.Fields, "eventTime")
	delete(t.Fields, "eventType")
	delete(t.Fields, "created_at")

	err = t.Valid(ctx)
	if err != nil {
		return nil, err
	}
	return t, nil
}

//func mgdlFromAny(log *slog.Logger, units string, value float64) int {
//	// Glucose meters work in range 0.6-33.3 mmol/l or 10-600 mg/dl.
//	// Do our best to fix bad data.
//
//	// zero value passthrough
//	if value == 0 {
//		return 0
//	}
//
//	if value < 0 {
//		log.Info("invalid (negative) BG ignored")
//		return 0
//	}
//	if value > 600 {
//		log.Info("invalid (high) BG ignored")
//		return 0
//	}
//
//	if units == "mmol" {
//		if value > 34 {
//			log.Info("treatment BG out of range, assuming mg/dl")
//			return int(value)
//		}
//		return int(value * 18)
//	}
//	if units == "mg/dl" {
//		if value < 10 {
//			log.Info("treatment BG out of range, assuming mmol/l")
//			return int(value * 18)
//		}
//		return int(value)
//	}
//	log.Info("unknown blood glucose units", slog.String("units", units))
//	return 0
//}

func parseTime(ts string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		// also try offset without a colon, as used by xDrip back-fill
		t, err = time.Parse("2006-01-02T15:04:05.999999999Z0700", ts)
		if err != nil {
			return t, ErrInvalidTimeString
		}
	}
	return t, nil
}

// TreatmentByOid not implemented by ns, but added for symmetry with entries api
func (a ApiV1) TreatmentByOid(w http.ResponseWriter, r *http.Request) {
	oid := chi.URLParam(r, "oid")
	ctx := r.Context()
	log := slogctx.FromCtx(ctx)

	treatment, err := a.FetchTreatmentByOid(ctx, oid)
	if err != nil {
		if errors.Is(err, models.ErrNotFound) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		log.Warn("treatmentService.ByID failed", slog.Any("error", err))
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	a.renderTreatmentList(w, r, []models.Treatment{*treatment})
}

func (a ApiV1) DeleteTreatment(w http.ResponseWriter, r *http.Request) {
	oid := chi.URLParam(r, "oid")
	ctx := r.Context()
	log := slogctx.FromCtx(ctx)

	err := a.DeleteTreatmentByOid(ctx, oid)
	if err != nil {
		if errors.Is(err, models.ErrNotFound) {
			// shuggah/xDrip will just retry if they get a 404, so return 200 here
			w.WriteHeader(http.StatusNoContent)
			return
		}
		log.Warn("cannot delete treatment", slog.Any("error", err))
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (a ApiV1) PutTreatment(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	log := slogctx.FromCtx(ctx)

	body, _ := io.ReadAll(r.Body)
	defer r.Body.Close()
	//fmt.Printf("%v\n", string(body))
	var reqTreatment map[string]any
	err := json.Unmarshal(body, &reqTreatment)
	if err != nil {
		log.Info("cannot unmarshal request body - invalid json?", slog.Any("err", err))
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	treatment, err := treatmentFromJSON(ctx, reqTreatment)
	if err != nil {
		if errors.Is(err, ErrInvalidTimeString) {
			log.Info("cannot parse treatment eventTime",
				slog.Any("eventTime", reqTreatment["eventTime"]),
				slog.Any("fields", reqTreatment),
			)
			http.Error(w, "unparseable treatment eventTime", http.StatusBadRequest)
			return
		}

		if errors.Is(err, models.ErrUnknownTreatmentType) {
			log.Info("unknown treatment type",
				slog.Any("entryType", reqTreatment["eventType"]),
				slog.Any("fields", reqTreatment),
			)
			http.Error(w, "unknown treatment type", http.StatusBadRequest)
			return
		}

		log.Info("invalid treatment",
			slog.Any("entryType", reqTreatment["eventType"]),
			slog.Any("err", err),
			slog.Any("fields", reqTreatment),
		)
		http.Error(w, "invalid treatment type", http.StatusBadRequest)
		return
	}

	err = a.TreatmentRepository.UpdateTreatmentByOid(ctx, treatment.ID, treatment)

	if err != nil {
		if errors.Is(err, models.ErrNotFound) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		log.Warn("cannot update treatment", slog.Any("error", err))
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
