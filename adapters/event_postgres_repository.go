package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"github.com/adamlounds/nightscout-go/models"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PostgresEventRepository struct {
	db *pgxpool.Pool
}

func NewPostgresEventRepository(db *pgxpool.Pool) *PostgresEventRepository {
	return &PostgresEventRepository{db: db}
}

func (p PostgresEventRepository) FetchEvent(ctx context.Context, id int) (*models.Event, error) {
	event := models.Event{
		ID: id,
	}

	row := p.db.QueryRow(ctx, "SELECT type, mgdl FROM events WHERE id = $1", id)
	err := row.Scan(&event.Type, &event.Mgdl)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, models.ErrNotFound
		}
		return nil, fmt.Errorf("pgevt ByID: %w", err)
	}
	return &event, nil
}
