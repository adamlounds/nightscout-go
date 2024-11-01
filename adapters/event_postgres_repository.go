package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"github.com/adamlounds/nightscout-go/models"
	pgstore "github.com/adamlounds/nightscout-go/stores/postgres"
)

type PostgresEventRepository struct {
	*pgstore.PostgresStore
}

func NewPostgresEventRepository(pgstore *pgstore.PostgresStore) *PostgresEventRepository {
	return &PostgresEventRepository{pgstore}
}

func (p PostgresEventRepository) FetchEvent(ctx context.Context, id int) (*models.Event, error) {
	event := models.Event{
		ID: id,
	}

	row := p.DB.QueryRow(ctx, "SELECT type, mgdl FROM events WHERE id = $1", id)
	err := row.Scan(&event.Type, &event.Mgdl)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, models.ErrNotFound
		}
		return nil, fmt.Errorf("pgevt ByID: %w", err)
	}
	return &event, nil
}
