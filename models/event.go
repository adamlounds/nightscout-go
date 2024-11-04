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
	ID          int
	Oid         string
	Type        string
	Mgdl        int // mg/dL value for this event
	Direction   string
	DeviceId    int
	CreatedTime time.Time
}

type EventService struct {
	EventRepository
}

func (s *EventService) ByID(ctx context.Context, id int) (*Event, error) {
	return s.FetchEvent(ctx, id)
}
