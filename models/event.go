package models

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5/pgxpool"
	"time"
)

type Event struct {
	ID        int       `json:"id"`
	Type      string    `json:"type"`
	Mgdl      int       `json:"mgdl"` // mg/dL value for this event
	Direction string    `json:"direction"`
	DeviceId  int       `json:"device_id"`
	CreatedAt time.Time `json:"created_at"`
}

type EventService struct {
	DB *pgxpool.Pool
}

func (s *EventService) ByID(ctx context.Context, id int) (*Event, error) {
	event := Event{
		ID: id,
	}

	row := s.DB.QueryRow(ctx, "SELECT type, mgdl FROM events WHERE id = $1", id)
	err := row.Scan(&event.Type, &event.Mgdl)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("eventSvc ByID: %w", err)
	}
	return &event, nil
}
