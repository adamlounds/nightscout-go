package repository

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/adamlounds/nightscout-go/models"
	bucketstore "github.com/adamlounds/nightscout-go/stores/bucket"
	pgstore "github.com/adamlounds/nightscout-go/stores/postgres"
	"github.com/jackc/pgx/v5"
	slogctx "github.com/veqryn/slog-context"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"
)

type device struct {
	ID          int
	Name        string
	CreatedTime time.Time
}

type entry struct {
	ID          int       `json:"id"`
	Oid         string    `json:"oid"`
	Type        string    `json:"type"`
	SgvMgdl     int       `json:"sgv_mgdl"`
	Trend       string    `json:"trend"`
	DeviceID    int       `json:"device_id"`
	EventTime   time.Time `json:"event_time"`
	CreatedTime time.Time `json:"created_time"`
}

type memStore struct {
	entries             []entry
	entriesLock         sync.Mutex
	deviceNamesByID     map[int]string
	deviceNamesByIDLock sync.Mutex
	entriesNeedSorting  bool
	dirtyDay            bool             // new entry today = update day file
	dirtyMonth          bool             // new entry this month (but not today): update month
	dirtyYears          map[int]struct{} // new entry outside of this month: update year file
	dirtyLock           sync.Mutex
}

type PostgresEntryRepository struct {
	*pgstore.PostgresStore
	*bucketstore.BucketStore
	memStore *memStore
}

func NewPostgresEntryRepository(pgstore *pgstore.PostgresStore, b *bucketstore.BucketStore) *PostgresEntryRepository {
	m := &memStore{
		deviceNamesByID: make(map[int]string),
		dirtyYears:      make(map[int]struct{}),
	}
	return &PostgresEntryRepository{pgstore, b, m}
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

	// WIP
	// NB: should it ignore future entries?
	if len(p.memStore.entries) > 1 {
		lastEntry := p.memStore.entries[len(p.memStore.entries)-1]
		return &models.Entry{
			ID:          lastEntry.ID,
			Oid:         lastEntry.Oid,
			Type:        lastEntry.Type,
			SgvMgdl:     lastEntry.SgvMgdl,
			Direction:   lastEntry.Trend,
			Device:      p.memStore.deviceNamesByID[lastEntry.DeviceID],
			Time:        lastEntry.EventTime,
			CreatedTime: lastEntry.CreatedTime,
		}, nil
	}

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

func (p PostgresEntryRepository) FetchAllDevices(ctx context.Context) ([]device, error) {
	rows, err := p.DB.Query(ctx, `SELECT id, name, created_time from device`)
	if err != nil {
		return nil, fmt.Errorf("pg FetchAllDevices: %w", err)
	}
	devices, err := pgx.CollectRows(rows, pgx.RowToStructByPos[device])
	if err != nil {
		return nil, fmt.Errorf("pg FetchAllDevices collect: %w", err)
	}

	p.memStore.deviceNamesByIDLock.Lock()
	defer p.memStore.deviceNamesByIDLock.Unlock()
	clear(p.memStore.deviceNamesByID)
	for _, device := range devices {
		p.memStore.deviceNamesByID[device.ID] = device.Name
	}

	return devices, nil
}

func (p PostgresEntryRepository) deviceIDsByName(ctx context.Context) (map[string]int, error) {
	devices, err := p.FetchAllDevices(ctx)
	if err != nil {
		return nil, fmt.Errorf("pg deviceIDsByName cannot fetch devices: %w", err)
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
		return nil, fmt.Errorf("pg InsertMissingDevices cannot fetch device ids: %w", err)
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
		return nil, fmt.Errorf("pg InsertMissingDevices cannot get inserted rows: %w", err)
	}
	for _, insertedDevice := range insertedDevices {
		knownDevices[insertedDevice.Name] = insertedDevice.ID
	}

	return knownDevices, nil
}

type storedEntry struct {
	Oid         string    `json:"_id"`
	Type        string    `json:"type"`
	SgvMgdl     int       `json:"sgv"`
	Direction   string    `json:"direction"`
	Device      string    `json:"device"`
	Time        time.Time `json:"dateString"`
	CreatedTime time.Time `json:"sysTime"`
}

// TODO: Events in the far future should end up in day file?

// syncToBucket will update any bucket objects that have been updated recently.
//
// Note currentTime arg is passed to avoid race condition around time boundaries.
// The year/month/day storage system is designed so we mostly update a single file,
// and at startup read four files (previous year, this year, this month, today).
// Data older than the previous year can be fetched on demand (?)
// They are designed such that reading those files will not have any duplicate/overlapping entries
func (p PostgresEntryRepository) syncToBucket(ctx context.Context, currentTime time.Time) {
	log := slogctx.FromCtx(ctx)
	log.Debug("syncing",
		slog.Time("time", currentTime),
		slog.Bool("dirtyDay", p.memStore.dirtyDay),
		slog.Bool("dirtyMonth", p.memStore.dirtyMonth),
		slog.Any("dirtyYears", p.memStore.dirtyYears),
	)

	p.syncDayToBucket(ctx, currentTime)
	p.syncMonthToBucket(ctx, currentTime)
	p.syncYearsToBucket(ctx, currentTime)
}

// syncMonthToBucket updates the day file in the object store.
// day files contain data for the current day
func (p PostgresEntryRepository) syncDayToBucket(ctx context.Context, currentTime time.Time) {
	log := slogctx.FromCtx(ctx)
	if !p.memStore.dirtyDay {
		return
	}
	log.Debug("syncing day",
		slog.Time("time", currentTime),
		slog.Bool("dirtyDay", p.memStore.dirtyDay),
	)

	var dayEntries []storedEntry
	currentYear := currentTime.Year()
	currentMonth := currentTime.Month()
	currentDay := currentTime.Day()
	for _, entry := range p.memStore.entries {
		if entry.EventTime.Year() != currentYear {
			continue
		}
		if entry.EventTime.Month() != currentMonth {
			continue
		}
		if entry.EventTime.Day() != currentDay {
			continue
		}
		dayEntries = append(dayEntries, storedEntry{
			entry.Oid,
			entry.Type,
			entry.SgvMgdl,
			entry.Trend,
			p.memStore.deviceNamesByID[entry.DeviceID],
			entry.EventTime,
			entry.CreatedTime,
		})
	}

	b, err := json.Marshal(dayEntries)
	if err != nil {
		log.Warn("cannot marshal day entries", slog.Any("err", err))
		return
	}

	r := bytes.NewReader(b)
	name := "dayfile.json"
	err = p.Upload(ctx, name, r)
	if err != nil {
		log.Warn("cannot upload day entries", slog.Any("err", err))
	}
	slog.Debug("uploaded day file", slog.String("name", name), slog.Int("size", len(b)))

}

// syncMonthToBucket updates a month file in the object store.
// month files contain data for the current month, excluding today
func (p PostgresEntryRepository) syncMonthToBucket(ctx context.Context, currentTime time.Time) {
	log := slogctx.FromCtx(ctx)
	if !p.memStore.dirtyMonth {
		return
	}
	log.Debug("syncing month",
		slog.Time("time", currentTime),
		slog.Bool("dirtyMonth", p.memStore.dirtyMonth),
	)
	var monthEntries []entry
	currentYear := currentTime.Year()
	currentMonth := currentTime.Month()
	currentDay := currentTime.Day()
	for _, entry := range p.memStore.entries {
		if entry.EventTime.Year() != currentYear {
			continue
		}
		if entry.EventTime.Month() != currentMonth {
			continue
		}
		// month files do not include today's data
		if entry.EventTime.Day() == currentDay {
			continue
		}
		monthEntries = append(monthEntries, entry)
	}
}

// syncMonthToBucket updates a year file in the object store.
// previous year files contain all data for that year.
// the current year file contain data for the current year, excluding this month.
func (p PostgresEntryRepository) syncYearsToBucket(ctx context.Context, currentTime time.Time) {
	log := slogctx.FromCtx(ctx)
	if len(p.memStore.dirtyYears) == 0 {
		return
	}
	log.Debug("syncing years",
		slog.Time("time", currentTime),
		slog.Any("dirtyYears", p.memStore.dirtyYears),
	)
	// TODO - this is more complex, we need to work on one year at a time,
	// and fetch data if it's for a year we don't already have in memory

	// for now, work on current year only?
	yearsEntries := make(map[int][]entry)
	currentYear := currentTime.Year()
	currentMonth := currentTime.Month()
	currentDay := currentTime.Day()
	for _, e := range p.memStore.entries {
		if e.EventTime.Year() == currentYear {
			if e.EventTime.Month() == currentMonth {
				// current "year" files do not include this month's data
				continue
			}
		}
		// month files do not include today's data
		if e.EventTime.Day() == currentDay {
			continue
		}
		_, ok := yearsEntries[e.EventTime.Year()]
		if !ok {
			yearsEntries[e.EventTime.Year()] = []entry{}
			continue
		}
		yearsEntries[e.EventTime.Year()] = append(yearsEntries[e.EventTime.Year()], e)
	}
}

// CreateEntries supports adding new entries to the db.
func (p PostgresEntryRepository) CreateEntries(ctx context.Context, entries []models.Entry) ([]models.Entry, error) {
	if len(entries) == 0 {
		return nil, nil
	}
	log := slogctx.FromCtx(ctx)
	now := time.Now()

	knownDevices, err := p.InsertMissingDevices(ctx, entries)
	if err != nil {
		return nil, fmt.Errorf("cannot insert missing devices: %w", err)
	}
	deviceNamesByID := make(map[int]string, len(knownDevices))
	for deviceName, deviceID := range knownDevices {
		deviceNamesByID[deviceID] = deviceName
	}

	// TODO de-dupe. We want to support bulk-import, maybe 250k entries (6mo),
	// so naive scan-all-existing-entries for each new entry may be bad
	if len(p.memStore.entries) > 0 {
		lastEntry := p.memStore.entries[len(p.memStore.entries)-1]
		// events within 10s should be checked for dupes
		lastEventTime := lastEntry.EventTime.Add(time.Second * 10)
		for _, entry := range entries {
			if entry.Time.Before(lastEventTime) {
				// potential dupe
				log.Info("potential dupe", slog.Time("evt", entry.Time), slog.Time("lastEvent", lastEventTime))
			}
		}
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
			// TODO look at reimplementing: mongo's sdk never resets the counter
			entry.Oid = primitive.NewObjectIDFromTimestamp(entry.Time).Hex()
		}

		deviceId, ok := knownDevices[entry.Device]
		if !ok {
			return nil, fmt.Errorf("pg CreateEntries cannot find device for %s", entry.Device)
		}

		valueArgs = append(valueArgs,
			entry.Oid,
			entry.Type,
			entry.SgvMgdl,
			entry.Direction,
			deviceId,
			entry.Time.UTC())
	}

	query := fmt.Sprintf("INSERT INTO entry (oid, type, sgv_mgdl, trend, device_id, entry_time) VALUES %s ON CONFLICT (oid) DO NOTHING RETURNING *",
		strings.Join(valueStrings, ","))

	rows, err := p.DB.Query(ctx, query, valueArgs...)
	if err != nil {
		return nil, fmt.Errorf("pg CreateEntries cannot insert: %w", err)
	}

	insertedEntries, err := pgx.CollectRows(rows, pgx.RowToStructByPos[entry])
	if err != nil {
		return nil, fmt.Errorf("pg CreateEntries cannot get inserted rows: %w", err)
	}

	var modelEntries []models.Entry
	p.memStore.entriesLock.Lock()
	defer p.memStore.entriesLock.Unlock()
	p.memStore.dirtyLock.Lock()
	defer p.memStore.dirtyLock.Unlock()
	var lastEventTime time.Time // last as in "furthest forward in time"
	if len(p.memStore.entries) > 0 {
		lastEventTime = p.memStore.entries[len(p.memStore.entries)-1].EventTime
	}
	entriesNeedSorting := false
	for _, insertedEntry := range insertedEntries {

		deviceName := deviceNamesByID[insertedEntry.DeviceID]
		modelEntries = append(modelEntries, models.Entry{
			ID:          insertedEntry.ID,
			Oid:         insertedEntry.Oid,
			Type:        insertedEntry.Type,
			SgvMgdl:     insertedEntry.SgvMgdl,
			Direction:   insertedEntry.Trend,
			Device:      deviceName,
			Time:        insertedEntry.EventTime,
			CreatedTime: insertedEntry.CreatedTime,
		})

		p.memStore.entries = append(p.memStore.entries, entry{
			Oid:         insertedEntry.Oid,
			Type:        insertedEntry.Type,
			SgvMgdl:     insertedEntry.SgvMgdl,
			Trend:       insertedEntry.Trend,
			DeviceID:    insertedEntry.DeviceID,
			EventTime:   insertedEntry.EventTime,
			CreatedTime: insertedEntry.CreatedTime,
		})

		if insertedEntry.EventTime.Before(lastEventTime) {
			entriesNeedSorting = true
		}

		if insertedEntry.EventTime.Day() == now.Day() {
			p.memStore.dirtyDay = true
		} else if insertedEntry.EventTime.Month() == now.Month() {
			p.memStore.dirtyMonth = true
		} else {
			p.memStore.dirtyYears[insertedEntry.EventTime.Year()] = struct{}{}
		}
		log.Info("inserted entry", "numEntries", len(p.memStore.entries))

		lastEventTime = insertedEntry.EventTime
	}

	if entriesNeedSorting {
		t1 := time.Now()
		slices.SortFunc(p.memStore.entries, func(a, b entry) int { return a.EventTime.Compare(b.EventTime) })
		log.Debug("entries sorted", slog.Int64("duration_us", time.Since(t1).Microseconds()))
	}

	if p.memStore.dirtyMonth || p.memStore.dirtyDay || len(p.memStore.dirtyYears) != 0 {
		syncContext := context.WithoutCancel(ctx)
		go p.syncToBucket(syncContext, now)
	}

	return modelEntries, nil
}
