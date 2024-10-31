package models

import (
	"context"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrNotFound = errors.New("models: no resource could be found")

type PostgresConfig struct {
	Host     string
	Port     string
	User     string
	Password string
	Database string
	SSLMode  string
}

// Connect will open at least one sql connection.
// Callers must ensure that the connection is closed via pool.Close()
func Connect(ctx context.Context, cfg PostgresConfig) (*pgxpool.Pool, error) {
	dbpool, err := pgxpool.New(ctx, cfg.String())
	if err != nil {
		return nil, fmt.Errorf("pg Connect: %w", err)
	}
	return dbpool, nil
}

func (cfg PostgresConfig) String() string {
	return fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=%s pool_min_conns=1",
		cfg.Host, cfg.Port, cfg.User, cfg.Password, cfg.Database, cfg.SSLMode)
}
