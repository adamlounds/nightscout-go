package nightscoutstore

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/adamlounds/nightscout-go/models"
	slogctx "github.com/veqryn/slog-context"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"time"
)

type NightscoutConfig struct {
	URL        *url.URL
	Token      string
	APISecret  string
	secretHash string
}

type NightscoutStore struct {
	URL        *url.URL
	Token      string
	SecretHash string
}

type nsEntry struct {
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

var ErrAccessDenied = errors.New("nsstore: permission denied")

func (cfg NightscoutConfig) String() string {
	return fmt.Sprintf("host=%s token=%s api_secret=%s", cfg.URL.String(), cfg.Token, cfg.APISecret)
}

func (cfg NightscoutConfig) SecretHash() string {
	if cfg.secretHash == "" {
		return cfg.secretHash
	}
	if cfg.APISecret == "" {
		return ""
	}
	h := sha1.New()
	h.Write([]byte(cfg.APISecret))
	cfg.secretHash = hex.EncodeToString(h.Sum(nil))
	return cfg.secretHash

}

func New(cfg NightscoutConfig) *NightscoutStore {

	return &NightscoutStore{
		URL:        cfg.URL,
		Token:      cfg.Token,
		SecretHash: cfg.SecretHash(),
	}
}

func (s *NightscoutStore) Ping(ctx context.Context) error {
	return nil
	//_, err := s.Nightscout.Exists(ctx, "ping")
	//return err
}

// FetchAllEntries fetches all possible entries from the remote nightscout
// instance, in reverse date order
func (s *NightscoutStore) FetchAllEntries(ctx context.Context) ([]models.Entry, error) {
	maxBatches := 100 // just in case something _weird_ happens, don't keep hammering remote server
	batchSize := 1000 // it may just be me, but my ns instance won't send more than 5417 entries, either in tsv or json...
	// another ns instance capped itself at 1152 entries, 258KB. Maybe investigate js side?

	lastEntry := models.Entry{}
	allEntries := []models.Entry{}
	for i := 0; i < maxBatches; i++ {
		batchOfEntries, err := s.fetchBatchOfEntries(ctx, batchSize, lastEntry)
		if err != nil {
			return nil, fmt.Errorf("cannot FetchAllEntries: %w", err)
		}

		allEntries = append(allEntries, batchOfEntries...)
		if len(batchOfEntries) < batchSize {
			return allEntries, nil
		}

		lastEntry = batchOfEntries[len(batchOfEntries)-1]
	}
	return allEntries, nil
}

var rfc3339msLayout = "2006-01-02T15:04:05.000Z"

// fetchEntryBatch fetches a batch of entries from a remote nightscout server
// notes: these will come in reverse date order, which is annoying from a
// "having to sort" perspective.
// For now, we are doing the simplest thing - fetch all entries and import them at once.
//
// if/when we want to address memory usage, we can think about methods to
// import in smaller batches. Maybe a two-pass approach - firstly determine how
// far back the remote nightscout server stores data (note auto-purge in ns is
// only 90 days), and keep track of the entries (or at least their timestamps)
// at the desired batch boundaries.
func (s *NightscoutStore) fetchBatchOfEntries(ctx context.Context, batchSize int, lastSeen models.Entry) ([]models.Entry, error) {
	log := slogctx.FromCtx(ctx)
	log.Debug("fetchEntryBatch called", slog.Int("batchSize", batchSize), slog.String("lastSeenTime", lastSeen.Time.Format(rfc3339msLayout)))

	u := *s.URL
	u.Path = path.Join(u.Path, "api", "v1", "entries.json")
	q := u.Query()
	q.Set("count", strconv.Itoa(batchSize))
	if s.Token != "" {
		q.Set("token", s.Token)
	}
	if s.SecretHash != "" {
		q.Set("secret", s.SecretHash)
	}

	if lastSeen.Time.IsZero() {
		// The first time fetchBatchOfEntries is called, it is passed a zero
		// models.Entry, with a zero time.
		// NB: there is an undocumented implicit limit of 4 days if no
		// explicit date range is specified in the query, so we set an explicit
		// range (date > 1970) in order to get a full batch of entries.
		q.Set("find[date][$gt]", "0")
	} else {
		// Subsequent calls pass the earliest-seen entry so we can ask
		// nightscout for entries before it.

		// nb using `lt` in the absence of deduping. Once dedupe sorted, we
		// should use `lte` instead.
		// As the code currently stands, we will lose entries that occur in the
		// same millisecond and happen to be on the batch boundary
		q.Set("find[date][$lt]", strconv.FormatInt(lastSeen.Time.UnixMilli(), 10))
	}

	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	req.Header.Add("User-Agent", "nightscout-go/0.3")
	if s.SecretHash != "" {
		req.Header.Add("api-secret", s.SecretHash)
	}
	if err != nil {
		return nil, fmt.Errorf("fetchBatchOfEntries cannot NewRequestWithContext: %w", err)
	}

	client := http.Client{}
	res, err := client.Do(req)
	if err != nil {
		var dnsError *net.DNSError
		if errors.As(err, &dnsError) {
			log.Info("fetchBatchOfEntries DNSError", slog.Any("err", dnsError))
			return nil, fmt.Errorf("fetchBatchOfEntries remote server NOT FOUND: %w", err)
		}
		return nil, fmt.Errorf("fetchBatchOfEntries cannot Do req: %w", err)
	}
	if res.StatusCode != 200 {
		log.Info("fetchBatchOfEntries got non-200 res", slog.Int("code", res.StatusCode), slog.String("url", u.String()))
		return nil, fmt.Errorf("fetchBatchOfEntries got non-200 response: %w", err)
	}

	var nsEntries []nsEntry
	err = json.NewDecoder(res.Body).Decode(&nsEntries)
	if err != nil {
		log.Info("fetchBatchOfEntries cannot parse entries", slog.Any("err", err))
		return nil, err
	}

	mEntries := make([]models.Entry, len(nsEntries))
	for i, e := range nsEntries {
		entryTime := time.UnixMilli(int64(e.Date))
		sysTime, err := time.Parse("2006-01-02T15:04:05.999Z", e.SysTime)
		if err != nil {
			sysTime = entryTime
		}
		mEntries[i] = models.Entry{
			Oid:         e.Oid,
			Type:        e.Type,
			SgvMgdl:     e.SgvMgdl,
			Direction:   e.Direction,
			Device:      e.Device,
			Time:        entryTime,
			CreatedTime: sysTime,
		}
	}

	log.Debug("fetchBatchOfEntries parsed entries",
		slog.Int("batchSize", batchSize),
		slog.Int("numEntriesParsed", len(mEntries)),
		slog.Time("latestEntry", mEntries[0].Time),
		slog.Time("earliestEntry", mEntries[len(mEntries)-1].Time),
	)

	if !lastSeen.Time.IsZero() {
		if !mEntries[len(mEntries)-1].Time.Before(lastSeen.Time) {
			// wtf? nightscout is not giving us older entries...
			slog.Warn("fetchBatchOfEntries remote ns not giving us older entries!")
			return []models.Entry{}, nil
		}
	}
	return mEntries, nil
}

func (b *NightscoutStore) IsAccessDeniedErr(err error) bool {
	return errors.Is(err, ErrAccessDenied)
}

func (b *NightscoutStore) IsObjNotFoundErr(err error) bool {
	return errors.Is(err, models.ErrNotFound)
}
