package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"github.com/adamlounds/nightscout-go/models"
	pgstore "github.com/adamlounds/nightscout-go/stores/postgres"
	"github.com/jackc/pgx/v5"
)

type PostgresEntryRepository struct {
	*pgstore.PostgresStore
}

func NewPostgresEntryRepository(pgstore *pgstore.PostgresStore) *PostgresEntryRepository {
	return &PostgresEntryRepository{pgstore}
}

func (p PostgresEntryRepository) FetchEntryByOid(ctx context.Context, oid string) (*models.Entry, error) {
	entry := models.Entry{
		Oid: oid,
	}

	row := p.DB.QueryRow(ctx, "SELECT id, type, sgv_mgdl, trend, created_time FROM entry WHERE oid = $1", oid)
	err := row.Scan(&entry.ID, &entry.Type, &entry.Mgdl, &entry.Direction, &entry.CreatedTime)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, models.ErrNotFound
		}
		return nil, fmt.Errorf("pg FetchEntryByOid: %w", err)
	}
	return &entry, nil
}

func (p PostgresEntryRepository) FetchLatestEntry(ctx context.Context) (*models.Entry, error) {
	var entry models.Entry

	row := p.DB.QueryRow(ctx, "SELECT id, oid, type, sgv_mgdl, trend, created_time FROM entry ORDER BY created_time DESC LIMIT 1")
	err := row.Scan(&entry.ID, &entry.Oid, &entry.Type, &entry.Mgdl, &entry.Direction, &entry.CreatedTime)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, models.ErrNotFound
		}
		return nil, fmt.Errorf("pg FetchLatestEntry: %w", err)
	}
	return &entry, nil
}

func (p PostgresEntryRepository) FetchLatestEntries(ctx context.Context, maxEntries int) ([]models.Entry, error) {
	rows, err := p.DB.Query(ctx, "SELECT id, oid, type, sgv_mgdl, trend, device_id, created_time FROM entry ORDER BY created_time DESC LIMIT $1", maxEntries)
	if err != nil {
		return nil, fmt.Errorf("pg FetchLatestEntries: %w", err)
	}
	entries, err := pgx.CollectRows(rows, pgx.RowToStructByPos[models.Entry])
	if err != nil {
		return nil, fmt.Errorf("pg FetchLatestEntries collect: %w", err)
	}
	return entries, nil
}
