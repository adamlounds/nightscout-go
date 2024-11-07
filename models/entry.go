package models

import (
	"context"
	"errors"
	"time"
)

var ErrNotFound = errors.New("models: no resource could be found")

type EntryRepository interface {
	FetchEntry(ctx context.Context, id int) (*Entry, error)
}

type Entry struct {
	ID          int
	Oid         string
	Type        string
	SgvMgdl     int // Sensor Glucose Value in mg/dL
	Direction   string
	DeviceId    int
	Time        time.Time
	CreatedTime time.Time
}

type EntryService struct {
	EntryRepository
}

func (s *EntryService) ByID(ctx context.Context, id int) (*Entry, error) {
	return s.FetchEntry(ctx, id)
}
