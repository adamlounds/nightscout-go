package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"github.com/adamlounds/nightscout-go/models"
	pgstore "github.com/adamlounds/nightscout-go/stores/postgres"
	"github.com/jackc/pgx/v5"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"strings"
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

	row := p.DB.QueryRow(ctx, "SELECT id, type, sgv_mgdl, trend, entry_time, created_time FROM entry WHERE oid = $1", oid)
	err := row.Scan(&entry.ID, &entry.Type, &entry.SgvMgdl, &entry.Direction, &entry.Time, &entry.CreatedTime)
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

	row := p.DB.QueryRow(ctx, "SELECT id, oid, type, sgv_mgdl, trend, entry_time, created_time FROM entry ORDER BY created_time DESC LIMIT 1")
	err := row.Scan(&entry.ID, &entry.Oid, &entry.Type, &entry.SgvMgdl, &entry.Direction, &entry.Time, &entry.CreatedTime)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, models.ErrNotFound
		}
		return nil, fmt.Errorf("pg FetchLatestEntry: %w", err)
	}
	return &entry, nil
}

func (p PostgresEntryRepository) FetchLatestEntries(ctx context.Context, maxEntries int) ([]models.Entry, error) {
	rows, err := p.DB.Query(ctx, "SELECT id, oid, type, sgv_mgdl, trend, device_id, entry_time, created_time FROM entry ORDER BY created_time DESC LIMIT $1", maxEntries)
	if err != nil {
		return nil, fmt.Errorf("pg FetchLatestEntries: %w", err)
	}
	entries, err := pgx.CollectRows(rows, pgx.RowToStructByPos[models.Entry])
	if err != nil {
		return nil, fmt.Errorf("pg FetchLatestEntries collect: %w", err)
	}
	return entries, nil
}

// CreateEntries supports adding new entries to the db.
func (p PostgresEntryRepository) CreateEntries(ctx context.Context, entries []models.Entry) error {
	if len(entries) == 0 {
		return nil
	}

	// Support multiple inserts via a single SQL query
	valueStrings := make([]string, 0, len(entries))
	valueArgs := make([]interface{}, 0, len(entries)*6)
	for i, entry := range entries {
		valueStrings = append(valueStrings, fmt.Sprintf("($%d, $%d, $%d, $%d, $%d, $%d)",
			i*6+1, i*6+2, i*6+3, i*6+4, i*6+5, i*6+6))

		if entry.Type == "" {
			entry.Type = "sgv"
		}

		if entry.Oid == "" {
			entry.Oid = primitive.NewObjectID().Hex()
		}

		valueArgs = append(valueArgs,
			entry.Oid,
			entry.Type,
			entry.SgvMgdl,
			entry.Direction,
			1, // TODO device ids?
			entry.Time)
	}

	query := fmt.Sprintf("INSERT INTO entry (oid, type, sgv_mgdl, trend, device_id, entry_time) VALUES %s ON CONFLICT (oid) DO NOTHING",
		strings.Join(valueStrings, ","))

	_, err := p.DB.Exec(ctx, query, valueArgs...)
	if err != nil {
		return fmt.Errorf("pg CreateEntries: %w", err)
	}

	return nil
}
