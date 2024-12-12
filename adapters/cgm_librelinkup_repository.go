package repository

import (
	"context"
	"github.com/adamlounds/nightscout-go/models"
	"github.com/adamlounds/nightscout-go/stores/cgmlibrelinkup"
	"time"
)

type LLUConfig struct {
	Region        string
	Password      string
	Username      string
	FetchInterval time.Duration
}

type LLUStore interface {
	FetchRecent(ctx context.Context, lastSeen time.Time) ([]models.Entry, error)
	ErrorIsAuthnFailed(error) bool
}

type CGMLibrelinkupRepository struct {
	config LLUConfig
	store  LLUStore
}

func NewCGMLibrelinkupRepository(cfg LLUConfig) *CGMLibrelinkupRepository {
	store := cgmlibrelinkup.New(&cgmlibrelinkup.LLUConfig{
		Username: cfg.Username,
		Password: cfg.Password,
		Region:   cfg.Region,
	})
	return &CGMLibrelinkupRepository{
		config: cfg,
		store:  store,
	}
}

func (r *CGMLibrelinkupRepository) IsConfigured() bool {
	return r.config.Password != "" && r.config.Username != ""
}

func (r *CGMLibrelinkupRepository) FetchRecent(ctx context.Context, lastSeen time.Time) ([]models.Entry, error) {
	return r.store.FetchRecent(ctx, lastSeen)
}

func (r *CGMLibrelinkupRepository) ErrorIsAuthnFailed(err error) bool {
	return r.store.ErrorIsAuthnFailed(err)
}
