package cgmlibrelinkup

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/adamlounds/nightscout-go/models"
	slogctx "github.com/veqryn/slog-context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"
)

var ErrAuthnFailed = errors.New("llu: authentication failed")
var ErrNoConnections = errors.New("llu: no connections found")
var ErrUnexpectedDataFormat = errors.New("llu: unexpected data format")
var ErrDownForMaintenance = errors.New("llu: servers down for maintenance")

var knownEndpoints = map[string]string{
	"ae":  "api-ae.libreview.io",
	"ap":  "api-ap.libreview.io",
	"au":  "api-au.libreview.io",
	"ca":  "api-ca.libreview.io",
	"de":  "api-de.libreview.io",
	"eu":  "api-eu.libreview.io",
	"eu2": "api-eu2.libreview.io",
	"fr":  "api-fr.libreview.io",
	"jp":  "api-jp.libreview.io",
	"us":  "api-us.libreview.io",
	"la":  "api-la.libreview.io",
	"ru":  "api.libreview.ru",
}

type LLUConfig struct {
	Username string
	Password string
	Region   string
}

type LLUStore struct {
	url               *url.URL
	config            LLUConfig
	UserID            string // account with view access, not always the patient
	accountID         string // derived from UserID
	PatientID         string
	authTicket        string
	authTicketExpires time.Time
	lastLogin         time.Time
	SensorID          string
	SensorSerial      string
	SensorStartTime   time.Time
}

func New(cfg *LLUConfig) *LLUStore {
	endpointString, ok := knownEndpoints[strings.ToLower(cfg.Region)]
	if !ok {
		endpointString = knownEndpoints["us"]
	}
	u, _ := url.Parse("https://" + endpointString)
	return &LLUStore{
		url:    u,
		config: *cfg,
	}
}

// FetchRecent fetches entries newer than `lastSeen`, in oldest-first order
// LibreLinkUp typically returns 48 data points:
// - 47 points are smoothed 15-minute interval readings covering the last 12 hours
// - The 48th point is the most recent real-time reading (1-minute interval)
// This means we get
// - Initial backfill of 12 hours of data at startup
// - Continuous real-time updates when polling every minute
func (s *LLUStore) FetchRecent(ctx context.Context, lastSeen time.Time) ([]models.Entry, error) {
	log := slogctx.FromCtx(ctx)
	log.Debug("fetching recent freshEntries from librelinkup")
	if s.authTicket == "" || s.UserID == "" || s.authTicketExpires.Before(time.Now()) {
		err := s.login(ctx)
		if err != nil {
			return nil, fmt.Errorf("cannot fetchRecent/login: %w", err)
		}
		err = s.connections(ctx)
		if err != nil {
			return nil, fmt.Errorf("cannot fetchRecent/connections: %w", err)
		}
	}

	sgvEntries, err := s.graph(ctx)
	if err != nil {
		return nil, fmt.Errorf("cannot fetchRecent/graph: %w", err)
	}

	var recentEntries []models.Entry
	for _, e := range sgvEntries {
		if !e.Time.After(lastSeen) {
			continue
		}

		recentEntries = append(recentEntries, e)
	}

	return recentEntries, nil
}

func (s *LLUStore) graph(ctx context.Context) ([]models.Entry, error) {
	log := slogctx.FromCtx(ctx)
	if s.PatientID == "" {
		log.Warn("lluStore graph called without PatientID")
		return nil, fmt.Errorf("PatientID is required")
	}
	u := *s.url
	u.Path = path.Join(u.Path, "llu", "connections", s.PatientID, "graph")

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		log.Warn("lluStore graph cannot NewRequestWithContext ", slog.Any("err", err))
		return nil, fmt.Errorf("lluStore graph cannot NewRequestWithContext: %w", err)
	}

	addLLUHeaders(req)
	s.addLLUAuthHeaders(req)

	client := http.Client{}
	res, err := client.Do(req)
	if err != nil {
		var dnsError *net.DNSError
		if errors.As(err, &dnsError) {
			log.Info("lluStore graph DNSError", slog.Any("err", dnsError))
			return nil, fmt.Errorf("lluStore graph remote server NOT FOUND: %w", err)
		}
		log.Info("lluStore graph cannot Do req", slog.Any("err", err))
		return nil, fmt.Errorf("lluStore graph cannot Do req: %w", err)
	}
	body, _ := io.ReadAll(res.Body)
	log.Debug("lluStore graph got response",
		slog.Int("code", res.StatusCode),
		slog.String("url", u.String()),
		slog.String("body", string(body)),
	)

	if res.StatusCode != 200 {
		if res.StatusCode == 911 {
			log.Debug("lluStore graph got 911 (maintenance) response")
			return nil, ErrDownForMaintenance
		}
		log.Info("lluStore graph got non-200 res",
			slog.Int("code", res.StatusCode),
			slog.String("url", u.String()),
			slog.Any("body", body),
		)
		return nil, fmt.Errorf("lluStore graph got non-200 response: %w", err)
	}

	var llugr lluGraphResponse
	err = json.Unmarshal(body, &llugr)
	if err != nil {
		log.Info("lluStore graph cannot Decode body", slog.Any("err", err))
		return nil, ErrUnexpectedDataFormat
	}

	if llugr.Status != 0 {
		log.Debug("lluStore graph failed, unexpected status", slog.Int("status", llugr.Status))
		return nil, ErrAuthnFailed
	}

	if len(llugr.Data.ActiveSensors) > 0 {
		sensor := llugr.Data.ActiveSensors[0].Sensor
		if s.SensorID != llugr.Data.ActiveSensors[0].Sensor.Sn {
			s.SensorID = sensor.DeviceID
			s.SensorSerial = sensor.Sn
			s.SensorStartTime = time.Unix(int64(sensor.A), 0).UTC()
		}
	}

	now := time.Now().UTC()
	var entries []models.Entry

	// historical readings at 15-minute intervals
	for _, e := range llugr.Data.GraphData {
		eventTime, err := time.Parse("1/2/2006 3:04:05 PM", e.Timestamp)
		if err != nil {
			log.Warn("lluStore graph cannot parse timestamp",
				slog.Any("err", err),
				slog.String("timestamp", e.Timestamp),
			)
			return nil, ErrUnexpectedDataFormat
		}

		// nb no trend available for historic data
		entries = append(entries, models.Entry{
			Type:        "sgv",
			SgvMgdl:     e.ValueInMgPerDl,
			Time:        eventTime,
			Device:      "llu ingester",
			CreatedTime: now,
		})
	}

	// latest reading available at 1-minute intervals
	latestReading := llugr.Data.Connection.GlucoseMeasurement
	if latestReading.Type != 1 {
		log.Debug("lluStore graph: unexpected measurement type", slog.Any("glucoseMeasurement", latestReading))
		return nil, ErrUnexpectedDataFormat
	}

	directionForTrendArrow := map[int]string{
		0: "NOT COMPUTABLE",
		1: "SingleDown",
		2: "FortyFiveDown",
		3: "Flat",
		4: "FortyFiveUp",
		5: "SingleUp",
	}

	trendString := directionForTrendArrow[latestReading.TrendArrow]
	latestTime, err := time.Parse("1/2/2006 3:04:05 PM", latestReading.Timestamp)
	if err != nil {
		log.Warn("lluStore graph cannot parse timestamp",
			slog.Any("err", err),
			slog.String("timestamp", latestReading.Timestamp),
		)
		return nil, ErrUnexpectedDataFormat
	}

	entries = append(entries, models.Entry{
		Type:        "sgv",
		SgvMgdl:     latestReading.ValueInMgPerDl,
		Direction:   trendString,
		Time:        latestTime,
		CreatedTime: now,
	})

	return entries, nil
}

type lluLoginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

func addLLUHeaders(req *http.Request) {

	req.Header.Add("Accept", "application/json, text/plain, */*")
	req.Header.Add("Content-Type", "application/json;charset=UTF-8")
	req.Header.Add("User-Agent", "Mozilla/5.0 (iPhone; CPU OS 17_4.1 like Mac OS X) AppleWebKit/536.26 (KHTML, like Gecko) Version/17.4.1 Mobile/10A5355d Safari/8536.25")
	req.Header.Add("version", "4.12.0")
	req.Header.Add("product", "llu.ios")
}

func (s *LLUStore) addLLUAuthHeaders(req *http.Request) {
	req.Header.Add("Authorization", "Bearer "+s.authTicket)
	req.Header.Add("Account-ID", s.accountID) // 4.11+
}

func (s *LLUStore) setUserID(userID string) {
	s.UserID = userID
	h := sha256.New()
	h.Write([]byte(userID))
	s.accountID = hex.EncodeToString(h.Sum(nil))
}

func (s *LLUStore) login(ctx context.Context) error {
	log := slogctx.FromCtx(ctx)
	u := *s.url
	u.Path = path.Join(u.Path, "llu", "auth", "login")

	reqBody, err := json.Marshal(lluLoginRequest{
		Email:    s.config.Username,
		Password: s.config.Password,
	})
	if err != nil {
		log.Warn("lluStore login cannot create login req json", slog.Any("err", err))
		return fmt.Errorf("lluStore login cannot create login req json: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(reqBody))
	if err != nil {
		log.Warn("lluStore login cannot NewRequestWithContext ", slog.Any("err", err))
		return fmt.Errorf("lluStore cannot NewRequestWithContext: %w", err)
	}

	addLLUHeaders(req)

	client := http.Client{}
	res, err := client.Do(req)
	if err != nil {
		var dnsError *net.DNSError
		if errors.As(err, &dnsError) {
			log.Info("lluStore DNSError", slog.Any("err", dnsError))
			return fmt.Errorf("lluStore remote server NOT FOUND: %w", err)
		}
		log.Info("lluStore login cannot Do req", slog.Any("err", err))
		return fmt.Errorf("lluStore cannot Do req: %w", err)
	}

	// may need to json.Unmarshal more than once. Reader is not a ReadSeeker
	// so will return empty on second Read attempt -> read into byte slice once
	// and re-use it.
	body, _ := io.ReadAll(res.Body)
	log.Debug("lluStore login got response",
		slog.Int("code", res.StatusCode),
		slog.String("url", u.String()),
		slog.String("body", string(body)),
	)

	// TODO: cope with 911 returned during maintenance
	if res.StatusCode != 200 {
		if res.StatusCode == 911 {
			log.Debug("lluStore login got 911 (maintenance) response")
			return ErrDownForMaintenance
		}

		log.Info("lluStore login got non-200 res",
			slog.Int("code", res.StatusCode),
			slog.String("url", u.String()),
			slog.Any("body", body),
		)
		return fmt.Errorf("lluStore got non-200 response: %w", err)
	}

	var regionRedirectResponse lluLoginRegionRedirectResponse
	err = json.Unmarshal(body, &regionRedirectResponse)
	if err != nil {
		log.Info("lluStore login cannot unmarshal wrong-region response", slog.Any("err", err),
			slog.Any("body", body))
		return fmt.Errorf("lluStore login cannot unmarshal wrong-region response: %w", err)
	}

	// known statuses: 0=OK, 2=auth fail, 4=tou not accepted
	if regionRedirectResponse.Status == 2 {
		log.Debug("lluStore login authn failed")
		return ErrAuthnFailed
	}

	if regionRedirectResponse.Data.Region != "" {
		if s.config.Region == regionRedirectResponse.Data.Region {
			log.Warn("lluStore login redirect loop",
				slog.String("origRegion", s.config.Region),
				slog.String("redirRegion", regionRedirectResponse.Data.Region),
				slog.String("url", u.String()),
			)
			return errors.New("lluStore login redirect loop")
		}
		log.Debug("lluStore login redirecting to another region",
			slog.String("origRegion", s.config.Region),
			slog.String("redirRegion", regionRedirectResponse.Data.Region),
		)
		s.setRegion(regionRedirectResponse.Data.Region)
		return s.login(ctx)
	}

	var loginResponse lluLoginResponse
	err = json.Unmarshal(body, &loginResponse)
	if err != nil {
		log.Info("lluStore login cannot unmarshal response", slog.Any("err", err))
		return fmt.Errorf("lluStore login cannot unmarshal response: %w", err)
	}

	if loginResponse.Data.AuthTicket.Token == "" {
		s.authTicket = ""
		s.authTicketExpires = time.Time{}
	}

	// nb tickets normally valid for 180 days
	s.lastLogin = time.Now()
	s.setUserID(loginResponse.Data.User.ID)
	s.authTicket = loginResponse.Data.AuthTicket.Token
	s.authTicketExpires = time.Unix(int64(loginResponse.Data.AuthTicket.Expires), 0).UTC()
	log.Debug("lluStore login: token obtained ok",
		slog.String("region", s.config.Region),
		slog.Time("expiry", s.authTicketExpires),
	)
	return nil
}

func (s *LLUStore) connections(ctx context.Context) error {
	log := slogctx.FromCtx(ctx)
	if s.authTicket == "" {
		log.Warn("lluStore connections called without authTicket")
		return fmt.Errorf("authTicket is required")
	}
	u := *s.url
	u.Path = path.Join(u.Path, "llu", "connections")

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		log.Warn("lluStore connections cannot NewRequestWithContext ", slog.Any("err", err))
		return fmt.Errorf("lluStore connections cannot NewRequestWithContext: %w", err)
	}

	addLLUHeaders(req)
	s.addLLUAuthHeaders(req)

	client := http.Client{}
	res, err := client.Do(req)
	if err != nil {
		var dnsError *net.DNSError
		if errors.As(err, &dnsError) {
			log.Info("lluStore connections DNSError", slog.Any("err", dnsError))
			return fmt.Errorf("lluStore connections remote server NOT FOUND: %w", err)
		}
		log.Info("lluStore connections cannot Do req", slog.Any("err", err))
		return fmt.Errorf("lluStore connections cannot Do req: %w", err)
	}
	body, _ := io.ReadAll(res.Body)
	log.Debug("lluStore connections got response",
		slog.Int("code", res.StatusCode),
		slog.String("url", u.String()),
		slog.String("body", string(body)),
	)

	if res.StatusCode != 200 {
		if res.StatusCode == 911 {
			log.Debug("lluStore connections got 911 (maintenance) response")
			return ErrDownForMaintenance
		}
		log.Info("lluStore connections got non-200 res",
			slog.Int("code", res.StatusCode),
			slog.String("url", u.String()),
			slog.Any("body", body),
		)
		return fmt.Errorf("lluStore connections got non-200 response: %w", err)
	}

	// e25e9a58-8c91-11ef-9073-d6090acef5fa

	var llucr lluConnectionsResponse
	err = json.Unmarshal(body, &llucr)
	if err != nil {
		log.Info("lluStore connections cannot Decode body",
			slog.Any("err", err),
		)
		return fmt.Errorf("lluStore connections cannot Decode body: %w", err)
	}

	if llucr.Status != 0 {
		log.Debug("lluStore connections failed, unexpected status",
			slog.Int("status", llucr.Status),
			slog.Any("body", body),
		)
		return ErrAuthnFailed
	}

	if len(llucr.Data) < 1 {
		log.Warn("lluStore connections: no connections found")
		return ErrNoConnections
	}

	//s.connectionID = llucr.Data[0].ID
	s.PatientID = llucr.Data[0].PatientId

	return nil
}

func (s *LLUStore) setRegion(region string) {
	s.config.Region = strings.ToLower(region)
	endpointString, ok := knownEndpoints[s.config.Region]
	if !ok {
		endpointString = knownEndpoints["eu2"]
	}
	u, _ := url.Parse("https://" + endpointString)
	s.url = u
}

func (s *LLUStore) ErrorIsAuthnFailed(err error) bool {
	return errors.Is(err, ErrAuthnFailed)
}

type lluLoginResponse struct {
	Status int `json:"status"`
	Data   struct {
		User struct {
			ID                    string `json:"id"`
			FirstName             string `json:"firstName"`
			LastName              string `json:"lastName"`
			Email                 string `json:"email"`
			Country               string `json:"country"`
			UiLanguage            string `json:"uiLanguage"`
			CommunicationLanguage string `json:"communicationLanguage"`
			AccountType           string `json:"accountType"`
			Uom                   string `json:"uom"`
			DateFormat            string `json:"dateFormat"`
			TimeFormat            string `json:"timeFormat"`
			EmailDay              []int  `json:"emailDay"`
			System                struct {
				Messages struct {
					AppReviewBanner                  int    `json:"appReviewBanner"`
					FirstUsePhoenix                  int    `json:"firstUsePhoenix"`
					FirstUsePhoenixReportsDataMerged int    `json:"firstUsePhoenixReportsDataMerged"`
					LluGettingStartedBanner          int    `json:"lluGettingStartedBanner"`
					LluNewFeatureModal               int    `json:"lluNewFeatureModal"`
					LluOnboarding                    int    `json:"lluOnboarding"`
					LvWebPostRelease                 string `json:"lvWebPostRelease"`
					StreamingTourMandatory           int    `json:"streamingTourMandatory"`
				} `json:"messages"`
			} `json:"system"`
			Details struct {
			} `json:"details"`
			TwoFactor struct {
				PrimaryMethod   string `json:"primaryMethod"`
				PrimaryValue    string `json:"primaryValue"`
				SecondaryMethod string `json:"secondaryMethod"`
				SecondaryValue  string `json:"secondaryValue"`
			} `json:"twoFactor"`
			Created   int `json:"created"`
			LastLogin int `json:"lastLogin"`
			Programs  struct {
			} `json:"programs"`
			DateOfBirth int `json:"dateOfBirth"`
			Practices   struct {
			} `json:"practices"`
			Devices struct {
			} `json:"devices"`
			Consents struct {
				Llu struct {
					PolicyAccept int `json:"policyAccept"`
					TouAccept    int `json:"touAccept"`
				} `json:"llu"`
			} `json:"consents"`
		} `json:"user"`
		Messages struct {
			Unread int `json:"unread"`
		} `json:"messages"`
		Notifications struct {
			Unresolved int `json:"unresolved"`
		} `json:"notifications"`
		AuthTicket struct {
			Token    string `json:"token"`
			Expires  int    `json:"expires"`
			Duration int64  `json:"duration"`
		} `json:"authTicket"`
		Invitations        interface{} `json:"invitations"`
		TrustedDeviceToken string      `json:"trustedDeviceToken"`
	} `json:"data"`
}

type lluLoginRegionRedirectResponse struct {
	Status int `json:"status"`
	Data   struct {
		Redirect bool   `json:"redirect"`
		Region   string `json:"region"`
	} `json:"data"`
}

type lluGraphResponse struct {
	Status int `json:"status"`
	Data   struct {
		Connection struct {
			ID         string `json:"id"`
			PatientID  string `json:"patientId"`
			Country    string `json:"country"`
			Status     int    `json:"status"`
			FirstName  string `json:"firstName"`
			LastName   string `json:"lastName"`
			TargetLow  int    `json:"targetLow"`
			TargetHigh int    `json:"targetHigh"`
			Uom        int    `json:"uom"`
			Sensor     struct {
				DeviceID string `json:"deviceId"`
				Sn       string `json:"sn"`
				A        int    `json:"a"`
				W        int    `json:"w"`
				Pt       int    `json:"pt"`
				S        bool   `json:"s"`
				Lj       bool   `json:"lj"`
			} `json:"sensor"`
			AlarmRules struct {
				H struct {
					On   bool    `json:"on"`
					Th   int     `json:"th"`
					Thmm float64 `json:"thmm"`
					D    int     `json:"d"`
					F    float64 `json:"f"`
				} `json:"h"`
				F struct {
					Th   int     `json:"th"`
					Thmm int     `json:"thmm"`
					D    int     `json:"d"`
					Tl   int     `json:"tl"`
					Tlmm float64 `json:"tlmm"`
				} `json:"f"`
				L struct {
					On   bool    `json:"on"`
					Th   int     `json:"th"`
					Thmm float64 `json:"thmm"`
					D    int     `json:"d"`
					Tl   int     `json:"tl"`
					Tlmm float64 `json:"tlmm"`
				} `json:"l"`
				Nd struct {
					I int `json:"i"`
					R int `json:"r"`
					L int `json:"l"`
				} `json:"nd"`
				P   int `json:"p"`
				R   int `json:"r"`
				Std struct {
				} `json:"std"`
			} `json:"alarmRules"`
			GlucoseMeasurement struct {
				FactoryTimestamp string      `json:"FactoryTimestamp"`
				Timestamp        string      `json:"Timestamp"`
				Type             int         `json:"type"`
				ValueInMgPerDl   int         `json:"ValueInMgPerDl"`
				TrendArrow       int         `json:"TrendArrow"`
				TrendMessage     interface{} `json:"TrendMessage"`
				MeasurementColor int         `json:"MeasurementColor"`
				GlucoseUnits     int         `json:"GlucoseUnits"`
				Value            float64     `json:"Value"`
				IsHigh           bool        `json:"isHigh"`
				IsLow            bool        `json:"isLow"`
			} `json:"glucoseMeasurement"`
			GlucoseItem struct {
				FactoryTimestamp string      `json:"FactoryTimestamp"`
				Timestamp        string      `json:"Timestamp"`
				Type             int         `json:"type"`
				ValueInMgPerDl   int         `json:"ValueInMgPerDl"`
				TrendArrow       int         `json:"TrendArrow"`
				TrendMessage     interface{} `json:"TrendMessage"`
				MeasurementColor int         `json:"MeasurementColor"`
				GlucoseUnits     int         `json:"GlucoseUnits"`
				Value            float64     `json:"Value"`
				IsHigh           bool        `json:"isHigh"`
				IsLow            bool        `json:"isLow"`
			} `json:"glucoseItem"`
			GlucoseAlarm  interface{} `json:"glucoseAlarm"`
			PatientDevice struct {
				Did                 string `json:"did"`
				Dtid                int    `json:"dtid"`
				V                   string `json:"v"`
				Ll                  int    `json:"ll"`
				H                   bool   `json:"h"`
				Hl                  int    `json:"hl"`
				U                   int    `json:"u"`
				FixedLowAlarmValues struct {
					Mgdl  int     `json:"mgdl"`
					Mmoll float64 `json:"mmoll"`
				} `json:"fixedLowAlarmValues"`
				Alarms            bool `json:"alarms"`
				FixedLowThreshold int  `json:"fixedLowThreshold"`
			} `json:"patientDevice"`
			Created int `json:"created"`
		} `json:"connection"`
		ActiveSensors []struct {
			Sensor struct {
				DeviceID string `json:"deviceId"`
				Sn       string `json:"sn"`
				A        int    `json:"a"`
				W        int    `json:"w"`
				Pt       int    `json:"pt"`
				S        bool   `json:"s"`
				Lj       bool   `json:"lj"`
			} `json:"sensor"`
			Device struct {
				Did                 string `json:"did"`
				Dtid                int    `json:"dtid"`
				V                   string `json:"v"`
				Ll                  int    `json:"ll"`
				H                   bool   `json:"h"`
				Hl                  int    `json:"hl"`
				U                   int    `json:"u"`
				FixedLowAlarmValues struct {
					Mgdl  int     `json:"mgdl"`
					Mmoll float64 `json:"mmoll"`
				} `json:"fixedLowAlarmValues"`
				Alarms            bool `json:"alarms"`
				FixedLowThreshold int  `json:"fixedLowThreshold"`
			} `json:"device"`
		} `json:"activeSensors"`
		GraphData []struct {
			FactoryTimestamp string  `json:"FactoryTimestamp"`
			Timestamp        string  `json:"Timestamp"`
			Type             int     `json:"type"`
			ValueInMgPerDl   int     `json:"ValueInMgPerDl"`
			MeasurementColor int     `json:"MeasurementColor"`
			GlucoseUnits     int     `json:"GlucoseUnits"`
			Value            float64 `json:"Value"`
			IsHigh           bool    `json:"isHigh"`
			IsLow            bool    `json:"isLow"`
		} `json:"graphData"`
	} `json:"data"`
	Ticket struct {
		Token    string `json:"token"`
		Expires  int    `json:"expires"`
		Duration int64  `json:"duration"`
	} `json:"ticket"`
}

type lluConnectionsResponse struct {
	Status int `json:"status"`
	Data   []struct {
		ID         string `json:"id"`
		PatientId  string `json:"patientId"`
		Country    string `json:"country"`
		Status     int    `json:"status"`
		FirstName  string `json:"firstName"`
		LastName   string `json:"lastName"`
		TargetLow  int    `json:"targetLow"`
		TargetHigh int    `json:"targetHigh"`
		Uom        int    `json:"uom"`
		Sensor     struct {
			DeviceID string `json:"deviceId"`
			Sn       string `json:"sn"`
			A        int    `json:"a"`
			W        int    `json:"w"`
			Pt       int    `json:"pt"`
			S        bool   `json:"s"`
			Lj       bool   `json:"lj"`
		} `json:"sensor"`
		AlarmRules struct {
			H struct {
				On   bool    `json:"on"`
				Th   int     `json:"th"`
				Thmm float64 `json:"thmm"`
				D    int     `json:"d"`
				F    float64 `json:"f"`
			} `json:"h"`
			F struct {
				Th   int     `json:"th"`
				Thmm int     `json:"thmm"`
				D    int     `json:"d"`
				Tl   int     `json:"tl"`
				Tlmm float64 `json:"tlmm"`
			} `json:"f"`
			L struct {
				On   bool    `json:"on"`
				Th   int     `json:"th"`
				Thmm float64 `json:"thmm"`
				D    int     `json:"d"`
				Tl   int     `json:"tl"`
				Tlmm float64 `json:"tlmm"`
			} `json:"l"`
			Nd struct {
				I int `json:"i"`
				R int `json:"r"`
				L int `json:"l"`
			} `json:"nd"`
			P   int `json:"p"`
			R   int `json:"r"`
			Std struct {
			} `json:"std"`
		} `json:"alarmRules"`
		GlucoseMeasurement struct {
			FactoryTimestamp string      `json:"FactoryTimestamp"`
			Timestamp        string      `json:"Timestamp"`
			Type             int         `json:"type"`
			ValueInMgPerDl   int         `json:"ValueInMgPerDl"`
			TrendArrow       int         `json:"TrendArrow"`
			TrendMessage     interface{} `json:"TrendMessage"`
			MeasurementColor int         `json:"MeasurementColor"`
			GlucoseUnits     int         `json:"GlucoseUnits"`
			Value            float64     `json:"Value"`
			IsHigh           bool        `json:"isHigh"`
			IsLow            bool        `json:"isLow"`
		} `json:"glucoseMeasurement"`
		GlucoseItem struct {
			FactoryTimestamp string      `json:"FactoryTimestamp"`
			Timestamp        string      `json:"Timestamp"`
			Type             int         `json:"type"`
			ValueInMgPerDl   int         `json:"ValueInMgPerDl"`
			TrendArrow       int         `json:"TrendArrow"`
			TrendMessage     interface{} `json:"TrendMessage"`
			MeasurementColor int         `json:"MeasurementColor"`
			GlucoseUnits     int         `json:"GlucoseUnits"`
			Value            float64     `json:"Value"`
			IsHigh           bool        `json:"isHigh"`
			IsLow            bool        `json:"isLow"`
		} `json:"glucoseItem"`
		GlucoseAlarm  interface{} `json:"glucoseAlarm"`
		PatientDevice struct {
			Did                 string `json:"did"`
			Dtid                int    `json:"dtid"`
			V                   string `json:"v"`
			Ll                  int    `json:"ll"`
			H                   bool   `json:"h"`
			Hl                  int    `json:"hl"`
			U                   int    `json:"u"`
			FixedLowAlarmValues struct {
				Mgdl  int     `json:"mgdl"`
				Mmoll float64 `json:"mmoll"`
			} `json:"fixedLowAlarmValues"`
			Alarms            bool `json:"alarms"`
			FixedLowThreshold int  `json:"fixedLowThreshold"`
		} `json:"patientDevice"`
		Created int `json:"created"`
	} `json:"data"`
	Ticket struct {
		Token    string `json:"token"`
		Expires  int    `json:"expires"`
		Duration int64  `json:"duration"`
	} `json:"ticket"`
}
