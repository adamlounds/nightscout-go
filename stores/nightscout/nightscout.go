package nightscoutstore

import (
	"context"
	"errors"
	"fmt"
	"github.com/adamlounds/nightscout-go/models"
	slogctx "github.com/veqryn/slog-context"
	"log/slog"
	"net/url"
)

type NightscoutConfig struct {
	URL       *url.URL
	Token     string
	APISecret string
}

type NightscoutStore struct {
	URL       *url.URL
	Token     string
	APISecret string
}

var ErrAccessDenied = errors.New("nsstore: permission denied")

func (cfg NightscoutConfig) String() string {
	return fmt.Sprintf("host=%s token=%s api_secret=%s", cfg.URL.String(), cfg.Token, cfg.APISecret)
}

func New(cfg NightscoutConfig) *NightscoutStore {
	return &NightscoutStore{
		URL:       cfg.URL,
		Token:     cfg.Token,
		APISecret: cfg.APISecret,
	}
}

func (b *NightscoutStore) Ping(ctx context.Context) error {
	return nil
	//_, err := b.Nightscout.Exists(ctx, "ping")
	//return err
}

func (b *NightscoutStore) FetchAllEntries(ctx context.Context) ([]models.Entry, error) {
	return b.fetchBatchOfEntries(ctx, 100, models.Entry{})
}

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
func (b *NightscoutStore) fetchBatchOfEntries(ctx context.Context, batchSize int, lastSeen models.Entry) ([]models.Entry, error) {
	log := slogctx.FromCtx(ctx)
	log.Debug("fetchEntryBatch called", slog.String("last Oid", lastSeen.Oid))

	if lastSeen.Oid != "" {
		return []models.Entry{}, nil
	}

	return []models.Entry{
		{
			Oid:     "faked1",
			Type:    "sgv",
			SgvMgdl: 100,
		},
		{
			Oid:     "faked2",
			Type:    "sgv",
			SgvMgdl: 102,
		},
	}, nil
}

func (b *NightscoutStore) IsAccessDeniedErr(err error) bool {
	return errors.Is(err, ErrAccessDenied)
}

func (b *NightscoutStore) IsObjNotFoundErr(err error) bool {
	return errors.Is(err, models.ErrNotFound)
}
