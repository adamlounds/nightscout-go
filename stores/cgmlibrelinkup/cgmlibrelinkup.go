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

var ErrAuthnFailed = errors.New("authentication failed")
var ErrNoConnections = errors.New("no connections found")

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
	connectionID      string
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

func (s *LLUStore) FetchRecent(ctx context.Context, lastSeen time.Time) ([]models.Entry, error) {
	log := slogctx.FromCtx(ctx)
	log.Debug("fetching recent entries from librelinkup")
	if s.authTicket == "" || s.connectionID == "" || s.authTicketExpires.Before(time.Now()) {
		err := s.login(ctx)
		if err != nil {
			return nil, fmt.Errorf("cannot fetchRecent/login: %w", err)
		}
		err = s.connections(ctx)
		if err != nil {
			return nil, fmt.Errorf("cannot fetchRecent/connections: %w", err)
		}
	}

	// TODO extract entries from response
	_, err := s.graph(ctx)
	if err != nil {
		return nil, fmt.Errorf("cannot fetchRecent/graph: %w", err)
	}

	return []models.Entry{
		{
			Oid:         "test-oid",
			Type:        "sgv",
			SgvMgdl:     100,
			Direction:   "Flat",
			Device:      "test-device",
			Time:        time.Now(),
			CreatedTime: time.Now(),
		},
	}, nil
}

func (s *LLUStore) graph(ctx context.Context) ([]models.Entry, error) {
	log := slogctx.FromCtx(ctx)
	if s.PatientID == "" {
		log.Warn("LLUStore graph called without PatientID")
		return nil, fmt.Errorf("PatientID is required")
	}
	u := *s.url
	u.Path = path.Join(u.Path, "llu", "connections", s.PatientID, "graph")

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		log.Warn("LLUStore graph cannot NewRequestWithContext ", slog.Any("err", err))
		return nil, fmt.Errorf("LLUStore graph cannot NewRequestWithContext: %w", err)
	}

	addLLUHeaders(req)
	s.addLLUAuthHeaders(req)

	client := http.Client{}
	res, err := client.Do(req)
	if err != nil {
		var dnsError *net.DNSError
		if errors.As(err, &dnsError) {
			log.Info("LLUStore graph DNSError", slog.Any("err", dnsError))
			return nil, fmt.Errorf("LLUStore graph remote server NOT FOUND: %w", err)
		}
		log.Info("LLUStore graph cannot Do req", slog.Any("err", err))
		return nil, fmt.Errorf("LLUStore graph cannot Do req: %w", err)
	}
	body, _ := io.ReadAll(res.Body)
	log.Debug("LLUStore graph got response",
		slog.Int("code", res.StatusCode),
		slog.String("url", u.String()),
		slog.String("body", string(body)),
	)

	if res.StatusCode != 200 {
		log.Info("LLUStore graph got non-200 res",
			slog.Int("code", res.StatusCode),
			slog.String("url", u.String()),
			slog.Any("body", body),
		)
		return nil, fmt.Errorf("LLUStore graph got non-200 response: %w", err)
	}
	if err != nil {
		return nil, fmt.Errorf("LLUStore graph failed: %w", err)
	}

	var llugr lluGraphResponse
	err = json.Unmarshal(body, &llugr)
	if err != nil {
		log.Info("LLUStore graph cannot Decode body", slog.Any("err", err))
		return nil, fmt.Errorf("LLUStore graph cannot Decode body: %w", err)
	}

	if llugr.Status != 0 {
		log.Debug("LLUStore graph failed, unexpected status", slog.Int("status", llugr.Status))
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

	return nil, nil
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
		log.Warn("LLUStore login cannot create login req json", slog.Any("err", err))
		return fmt.Errorf("LLUStore login cannot create login req json: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(reqBody))
	if err != nil {
		log.Warn("LLUStore login cannot NewRequestWithContext ", slog.Any("err", err))
		return fmt.Errorf("LLUStore cannot NewRequestWithContext: %w", err)
	}

	addLLUHeaders(req)
	//req.Header.Add("Content-Type", "application/json;charset=UTF-8")

	client := http.Client{}
	res, err := client.Do(req)
	if err != nil {
		var dnsError *net.DNSError
		if errors.As(err, &dnsError) {
			log.Info("LLUStore DNSError", slog.Any("err", dnsError))
			return fmt.Errorf("LLUStore remote server NOT FOUND: %w", err)
		}
		log.Info("LLUStore login cannot Do req", slog.Any("err", err))
		return fmt.Errorf("LLUStore cannot Do req: %w", err)
	}

	// may need to json.Unmarshal more than once. Reader is not a ReadSeeker
	// so will return empty on second Read attempt -> read into byte slice once
	// and re-use it.
	body, _ := io.ReadAll(res.Body)
	log.Debug("LLUStore login got response",
		slog.Int("code", res.StatusCode),
		slog.String("url", u.String()),
		slog.String("body", string(body)),
	)

	if res.StatusCode != 200 {
		log.Info("LLUStore login got non-200 res",
			slog.Int("code", res.StatusCode),
			slog.String("url", u.String()),
			slog.Any("body", body),
		)
		return fmt.Errorf("LLUStore got non-200 response: %w", err)
	}

	var regionRedirectResponse lluLoginRegionRedirectResponse
	err = json.Unmarshal(body, &regionRedirectResponse)
	if err != nil {
		log.Info("LLUStore login cannot unmarshal wrong-region response", slog.Any("err", err),
			slog.Any("body", body))
		return fmt.Errorf("LLUStore login cannot unmarshal wrong-region response: %w", err)
	}

	if regionRedirectResponse.Status == 2 {
		log.Debug("LLUStore login authn failed")
		return ErrAuthnFailed
	}

	if regionRedirectResponse.Data.Region != "" {
		if s.config.Region == regionRedirectResponse.Data.Region {
			log.Warn("LLUStore login redirect loop",
				slog.String("origRegion", s.config.Region),
				slog.String("redirRegion", regionRedirectResponse.Data.Region),
				slog.String("url", u.String()),
			)
			return errors.New("LLUStore login redirect loop?")
		}
		log.Debug("LLUStore login redirecting to another region",
			slog.String("origRegion", s.config.Region),
			slog.String("redirRegion", regionRedirectResponse.Data.Region),
		)
		s.setRegion(regionRedirectResponse.Data.Region)
		return s.login(ctx)
	}

	var loginResponse lluLoginResponse
	err = json.Unmarshal(body, &loginResponse)
	if err != nil {
		log.Info("LLUStore login cannot unmarshal response", slog.Any("err", err))
		return fmt.Errorf("LLUStore login cannot unmarshal response: %w", err)
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
	log.Debug("LLUStore login: token obtained ok",
		slog.String("region", s.config.Region),
		slog.Time("expiry", s.authTicketExpires),
	)
	return nil
}

func (s *LLUStore) connections(ctx context.Context) error {
	log := slogctx.FromCtx(ctx)
	if s.authTicket == "" {
		log.Warn("LLUStore connections called without authTicket")
		return fmt.Errorf("authTicket is required")
	}
	u := *s.url
	u.Path = path.Join(u.Path, "llu", "connections")

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		log.Warn("LLUStore connections cannot NewRequestWithContext ", slog.Any("err", err))
		return fmt.Errorf("LLUStore connections cannot NewRequestWithContext: %w", err)
	}

	addLLUHeaders(req)
	s.addLLUAuthHeaders(req)

	client := http.Client{}
	res, err := client.Do(req)
	if err != nil {
		var dnsError *net.DNSError
		if errors.As(err, &dnsError) {
			log.Info("LLUStore connections DNSError", slog.Any("err", dnsError))
			return fmt.Errorf("LLUStore connections remote server NOT FOUND: %w", err)
		}
		log.Info("LLUStore connections cannot Do req", slog.Any("err", err))
		return fmt.Errorf("LLUStore connections cannot Do req: %w", err)
	}
	body, _ := io.ReadAll(res.Body)
	log.Debug("LLUStore connections got response",
		slog.Int("code", res.StatusCode),
		slog.String("url", u.String()),
		slog.String("body", string(body)),
	)

	if res.StatusCode != 200 {
		log.Info("LLUStore connections got non-200 res",
			slog.Int("code", res.StatusCode),
			slog.String("url", u.String()),
			slog.Any("body", body),
		)
		return fmt.Errorf("LLUStore connections got non-200 response: %w", err)
	}

	// e25e9a58-8c91-11ef-9073-d6090acef5fa

	var llucr lluConnectionsResponse
	err = json.Unmarshal(body, &llucr)
	if err != nil {
		log.Info("LLUStore connections cannot Decode body",
			slog.Any("err", err),
		)
		return fmt.Errorf("LLUStore connections cannot Decode body: %w", err)
	}

	if llucr.Status != 0 {
		log.Debug("LLUStore connections failed, unexpected status",
			slog.Int("status", llucr.Status),
			slog.Any("body", body),
		)
		return ErrAuthnFailed
	}

	if len(llucr.Data) < 1 {
		log.Warn("LLUStore connections: no connections found")
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
		endpointString, _ = knownEndpoints["eu2"]
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
