package repository

import (
	"context"
	"github.com/adamlounds/nightscout-go/models"
	nightscoutstore "github.com/adamlounds/nightscout-go/stores/nightscout"
	"net/url"
)

type NightscoutRepository struct{}

type NightscoutConfig struct {
	URL        *url.URL
	Token      string
	APISecret  string
	secretHash string
}

func NewNightscoutRepository() *NightscoutRepository {
	return &NightscoutRepository{}
}

func (b *NightscoutRepository) FetchAllEntries(ctx context.Context, nsCfg NightscoutConfig) ([]models.Entry, error) {
	store := nightscoutstore.New(nightscoutstore.NightscoutConfig{
		URL:       nsCfg.URL,
		Token:     nsCfg.Token,
		APISecret: nsCfg.APISecret,
	})
	return store.FetchAllEntries(ctx)
}
