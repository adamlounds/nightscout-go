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
	"time"
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

	row := p.DB.QueryRow(ctx, `SELECT
	e.id, e.oid, e.type, e.sgv_mgdl, e.trend, d.name, e.entry_time, e.created_time
	FROM entry e, device d 
	WHERE e.device_id = d.id AND oid = $1`, oid)
	err := row.Scan(&entry.ID, &entry.Oid, &entry.Type, &entry.SgvMgdl, &entry.Direction, &entry.Device, &entry.Time, &entry.CreatedTime)
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

	row := p.DB.QueryRow(ctx, `SELECT
	e.id, e.oid, e.type, e.sgv_mgdl, e.trend, d.name, e.entry_time, e.created_time
	FROM entry e, device d 
	WHERE e.device_id = d.id
	ORDER BY created_time DESC LIMIT 1`)
	err := row.Scan(&entry.ID, &entry.Oid, &entry.Type, &entry.SgvMgdl, &entry.Direction, &entry.Device, &entry.Time, &entry.CreatedTime)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, models.ErrNotFound
		}
		return nil, fmt.Errorf("pg FetchLatestEntry: %w", err)
	}
	return &entry, nil
}

func (p PostgresEntryRepository) FetchLatestEntries(ctx context.Context, maxEntries int) ([]models.Entry, error) {
	rows, err := p.DB.Query(ctx, `SELECT
	e.id, e.oid, e.type, e.sgv_mgdl, e.trend, d.name, e.entry_time, e.created_time
	FROM entry e, device d
	WHERE e.device_id = d.id
	ORDER BY created_time DESC LIMIT $1`, maxEntries)
	if err != nil {
		return nil, fmt.Errorf("pg FetchLatestEntries: %w", err)
	}
	entries, err := pgx.CollectRows(rows, pgx.RowToStructByPos[models.Entry])
	if err != nil {
		return nil, fmt.Errorf("pg FetchLatestEntries collect: %w", err)
	}
	return entries, nil
}

type device struct {
	ID          int
	Name        string
	CreatedTime time.Time
}

func (p PostgresEntryRepository) FetchAllDevices(ctx context.Context) ([]device, error) {
	rows, err := p.DB.Query(ctx, `SELECT id, name, created_time from device`)
	if err != nil {
		return nil, fmt.Errorf("pg FetchAllDevices: %w", err)
	}
	devices, err := pgx.CollectRows(rows, pgx.RowToStructByPos[device])
	if err != nil {
		return nil, fmt.Errorf("pg FetchLatestEntries collect: %w", err)
	}
	return devices, nil
}

func (p PostgresEntryRepository) deviceIDsByName(ctx context.Context) (map[string]int, error) {
	devices, err := p.FetchAllDevices(ctx)
	if err != nil {
		return nil, fmt.Errorf("pg deviceIDsByName: %w", err)
	}
	deviceIDsByName := make(map[string]int, len(devices))
	for _, device := range devices {
		deviceIDsByName[device.Name] = device.ID
	}
	return deviceIDsByName, nil
}

func (p PostgresEntryRepository) InsertMissingDevices(ctx context.Context, entries []models.Entry) (map[string]int, error) {
	knownDevices, err := p.deviceIDsByName(ctx)
	if err != nil {
		return nil, fmt.Errorf("pg CreateEntries cannot fetch devices: %w", err)
	}

	var devicesToAdd []string
	for _, entry := range entries {
		_, ok := knownDevices[entry.Device]
		if !ok {
			devicesToAdd = append(devicesToAdd, entry.Device)
		}
	}

	if len(devicesToAdd) == 0 {
		return knownDevices, nil
	}

	// Support multiple inserts via a single SQL query
	placeholders := make([]string, len(devicesToAdd))
	valueArgs := make([]interface{}, len(devicesToAdd))
	for i, deviceName := range devicesToAdd {
		placeholders[i] = fmt.Sprintf("($%d)", i+1)
		valueArgs[i] = deviceName
	}

	query := fmt.Sprintf("INSERT INTO device (name) VALUES %s ON CONFLICT (name) DO NOTHING RETURNING *;",
		strings.Join(placeholders, ","))
	rows, err := p.DB.Query(ctx, query, valueArgs...)
	if err != nil {
		return nil, fmt.Errorf("pg InsertMissingDevices cannot insert: %w", err)
	}

	insertedDevices, err := pgx.CollectRows(rows, pgx.RowToStructByPos[device])
	if err != nil {
		return nil, fmt.Errorf("pg InsertMissingDevices cannot get returned ids: %w", err)
	}
	for _, insertedDevice := range insertedDevices {
		knownDevices[insertedDevice.Name] = insertedDevice.ID
	}

	return knownDevices, nil
}

// CreateEntries supports adding new entries to the db.
func (p PostgresEntryRepository) CreateEntries(ctx context.Context, entries []models.Entry) error {
	if len(entries) == 0 {
		return nil
	}

	knownDevices, err := p.InsertMissingDevices(ctx, entries)
	if err != nil {
		return fmt.Errorf("cannot createEntries: %w", err)
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

		deviceId, ok := knownDevices[entry.Device]
		if !ok {
			return fmt.Errorf("pg CreateEntries cannot find device for %s", entry.Device)
		}

		valueArgs = append(valueArgs,
			entry.Oid,
			entry.Type,
			entry.SgvMgdl,
			entry.Direction,
			deviceId,
			entry.Time)
	}

	query := fmt.Sprintf("INSERT INTO entry (oid, type, sgv_mgdl, trend, device_id, entry_time) VALUES %s ON CONFLICT (oid) DO NOTHING",
		strings.Join(valueStrings, ","))

	_, err = p.DB.Exec(ctx, query, valueArgs...)
	if err != nil {
		return fmt.Errorf("pg CreateEntries: %w", err)
	}

	return nil
}
