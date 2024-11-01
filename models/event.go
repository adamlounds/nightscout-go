package models

import (
	"context"
	"errors"
	"time"
)

var ErrNotFound = errors.New("models: no resource could be found")

type EventRepository interface {
	FetchEvent(ctx context.Context, id int) (*Event, error)
}

type Event struct {
	ID        int       `json:"id"`
	Type      string    `json:"type"`
	Mgdl      int       `json:"mgdl"` // mg/dL value for this event
	Direction string    `json:"direction"`
	DeviceId  int       `json:"device_id"`
	CreatedAt time.Time `json:"created_at"`
}

type EventService struct {
	EventRepository
}

func (s *EventService) ByID(ctx context.Context, id int) (*Event, error) {
	return s.FetchEvent(ctx, id)
}
