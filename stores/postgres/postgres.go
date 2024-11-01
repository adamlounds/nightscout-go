package pgstore

import (
	"context"
	"fmt"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PostgresConfig struct {
	Host     string
	Port     string
	User     string
	Password string
	Database string
	SSLMode  string
}

func (cfg PostgresConfig) String() string {
	return fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=%s pool_min_conns=1",
		cfg.Host, cfg.Port, cfg.User, cfg.Password, cfg.Database, cfg.SSLMode)
}

type PostgresStore struct {
	DB *pgxpool.Pool
}

func New(cfg PostgresConfig) (*PostgresStore, error) {
	db, err := pgxpool.New(context.Background(), cfg.String())
	if err != nil {
		fmt.Printf("urgh %v", err)
		return nil, fmt.Errorf("run cannot set up db: %w", err)
	}
	return &PostgresStore{DB: db}, nil
}

func (p *PostgresStore) Close() {
	p.DB.Close()
}

func (p *PostgresStore) Ping(ctx context.Context) error {
	var pgVersion string
	err := p.DB.QueryRow(ctx, "select version()").Scan(&pgVersion)
	if err != nil {
		return fmt.Errorf("pg cannot ping db: %w", err)
	}
	fmt.Printf("pg Ping ok [%s]\n", pgVersion)
	return nil

}
