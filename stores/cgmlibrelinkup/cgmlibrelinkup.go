package cgmlibrelinkup

import (
	"bytes"
	"context"
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
	authTicket        string
	authTicketExpires time.Time
	lastLogin         time.Time
}

func New(cfg *LLUConfig) *LLUStore {
	endpointString, ok := knownEndpoints[strings.ToLower(cfg.Region)]
	if !ok {
		endpointString, _ = knownEndpoints["us"]
	}
	u, _ := url.Parse("https://" + endpointString)
	return &LLUStore{
		url:    u,
		config: *cfg,
	}
}

func (s *LLUStore) FetchRecent(ctx context.Context, lastSeen time.Time) ([]models.Entry, error) {
	if s.authTicket == "" {
		err := s.login(ctx)
		if err != nil {
			return nil, fmt.Errorf("cannot fetchRecent/login: %w", err)
		}
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

type lluLoginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
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
		return fmt.Errorf("LLUStore login cannot create login req json: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(reqBody))
	if err != nil {
		return fmt.Errorf("LLUStore login cannot NewRequestWithContext: %w", err)
	}
	req.Header.Add("User-Agent", "Mozilla/5.0 (iPhone; CPU OS 17_4.1 like Mac OS X) AppleWebKit/536.26 (KHTML, like Gecko) Version/17.4.1 Mobile/10A5355d Safari/8536.25")
	req.Header.Add("Content-Type", "application/json;charset=UTF-8")
	req.Header.Add("Version", "4.12.0")
	req.Header.Add("Product", "llu.ios")

	client := http.Client{}
	res, err := client.Do(req)
	if err != nil {
		var dnsError *net.DNSError
		if errors.As(err, &dnsError) {
			log.Info("LLUStore login DNSError", slog.Any("err", dnsError))
			return fmt.Errorf("LLUStore login remote server NOT FOUND: %w", err)
		}
		return fmt.Errorf("LLUStore login cannot Do req: %w", err)
	}
	if res.StatusCode != 200 {
		log.Info("LLUStore login got non-200 res", slog.Int("code", res.StatusCode), slog.String("url", u.String()))
		return fmt.Errorf("LLUStore login got non-200 response: %w", err)
	}

	// may need to json.Decode more than once. Reader is not a ReadSeeker
	// so will return empty on second Read attempt. Read into byte slice once
	// and re-use it.
	jsonBody, _ := io.ReadAll(res.Body)

	var regionRedirectResponse lluLoginRegionRedirectResponse
	err = json.Unmarshal(jsonBody, &regionRedirectResponse)
	if err != nil {
		return fmt.Errorf("LLUStore login cannot unmarshal wrong-region response: %w", err)
	}

	if regionRedirectResponse.Status == 2 {
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
	err = json.Unmarshal(jsonBody, &loginResponse)
	if err != nil {
		return fmt.Errorf("LLUStore login cannot unmarshal response: %w", err)
	}

	if loginResponse.Data.AuthTicket.Token == "" {
		s.authTicket = ""
		s.authTicketExpires = time.Time{}
	}

	// nb tickets normally valid for 180 days
	s.lastLogin = time.Now()
	s.authTicket = loginResponse.Data.AuthTicket.Token
	s.authTicketExpires = time.Unix(int64(loginResponse.Data.AuthTicket.Expires), 0)
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
			Id                    string `json:"id"`
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
