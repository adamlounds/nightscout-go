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

type PostgresEventRepository struct {
	*pgstore.PostgresStore
}

func NewPostgresEventRepository(pgstore *pgstore.PostgresStore) *PostgresEventRepository {
	return &PostgresEventRepository{pgstore}
}

func (p PostgresEventRepository) FetchEventByOid(ctx context.Context, oid string) (*models.Event, error) {
	event := models.Event{
		Oid: oid,
	}

	row := p.DB.QueryRow(ctx, "SELECT id, type, mgdl, trend, created_time FROM events WHERE oid = $1", oid)
	err := row.Scan(&event.ID, &event.Type, &event.Mgdl, &event.Direction, &event.CreatedTime)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, models.ErrNotFound
		}
		return nil, fmt.Errorf("pg FetchEventByOid: %w", err)
	}
	return &event, nil
}

func (p PostgresEventRepository) FetchLatestEvent(ctx context.Context) (*models.Event, error) {
	var event models.Event

	row := p.DB.QueryRow(ctx, "SELECT id, oid, type, mgdl, trend, created_time FROM events ORDER BY created_time DESC LIMIT 1")
	err := row.Scan(&event.ID, &event.Oid, &event.Type, &event.Mgdl, &event.Direction, &event.CreatedTime)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, models.ErrNotFound
		}
		return nil, fmt.Errorf("pg FetchLatestEvent: %w", err)
	}
	return &event, nil
}

func (p PostgresEventRepository) FetchLatestEvents(ctx context.Context, maxEvents int) ([]models.Event, error) {
	rows, err := p.DB.Query(ctx, "SELECT id, oid, type, mgdl, trend, device_id, created_time FROM events ORDER BY created_time DESC LIMIT $1", maxEvents)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, models.ErrNotFound
		}
		return nil, fmt.Errorf("pg FetchLatestEvents: %w", err)
	}
	events, err := pgx.CollectRows(rows, pgx.RowToStructByPos[models.Event])
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			panic("collectRows errored on no rows???")
			return nil, models.ErrNotFound
		}
		return nil, fmt.Errorf("pg FetchLatestEvents collect: %w", err)
	}
	return events, nil
}
