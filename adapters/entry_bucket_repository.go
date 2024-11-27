package repository

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/adamlounds/nightscout-go/models"
	slogctx "github.com/veqryn/slog-context"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"io"
	"log/slog"
	"slices"
	"sync"
	"time"
)

// TODO look at optimising in-memory usage. There is overlap between Oid
// and createdTime, and we probably don't need to store ns-resolution for
// createdTime. Nothing should be introspecting Oid or expecting it to
// include an unchanging device id, so we could use/store 64-bit
// Time.Now().UnixMicro() plus a 32-bit counter & generate Oids on demand from
// those.
// current: time (8bytes) + oid string ( 24 + 16 header = 40 bytes) = 48 bytes
// proposed: time (8 bytes) + oid counter (4 bytes) = 12 bytes
// type enum(4) and trend (enum 10) should be uint8 or smaller.
type memEntry struct {
	EventTime   time.Time
	CreatedTime time.Time
	Oid         string
	Type        string
	Trend       string
	SgvMgdl     int
	DeviceID    int
}

type memStore struct {
	deviceNames     []string
	deviceIDsByName map[string]int
	dirtyYears      map[int]struct{} // new memEntry outside of this month: update year file
	entries         []memEntry
	entriesLock     sync.Mutex
	deviceNamesLock sync.Mutex
	dirtyLock       sync.Mutex
	dirtyDay        bool // new memEntry today = update day file
	dirtyMonth      bool // new memEntry this month (but not today): update month
}

type BucketStoreInterface interface {
	Get(ctx context.Context, file string) (io.ReadCloser, error)
	Upload(ctx context.Context, name string, r io.Reader) error
	IsObjNotFoundErr(err error) bool
	IsAccessDeniedErr(err error) bool
}

type BucketEntryRepository struct {
	BucketStore BucketStoreInterface
	memStore    *memStore
}

func NewBucketEntryRepository(bs BucketStoreInterface) *BucketEntryRepository {
	m := &memStore{
		deviceNames: []string{"unknown"},
		deviceIDsByName: map[string]int{
			"unknown": 0,
		},
		dirtyYears: make(map[int]struct{}),
	}
	return &BucketEntryRepository{bs, m}
}

// Boot fetches common data into memory, typically at server startup
func (p BucketEntryRepository) Boot(ctx context.Context) error {
	log := slogctx.FromCtx(ctx)

	// loading files in order means we don't have to sort afterwards.
	now := time.Now()
	entryFiles := map[string]string{
		"prevYear": fmt.Sprintf("ns-year/%d.json", now.Year()-1),
		"year":     fmt.Sprintf("ns-year/%d.json", now.Year()),
		"month":    fmt.Sprintf("ns-month/%s.json", now.Format("2006-01")),
		"day":      fmt.Sprintf("ns-day/%s.json", now.Format("2006-01-02")),
	}

	for name, file := range entryFiles {
		err := p.fetchEntries(ctx, file)
		if err != nil {
			if p.BucketStore.IsObjNotFoundErr(err) {
				log.Debug("boot: cannot find file (not written yet?)",
					slog.String("name", name),
					slog.String("file", file),
				)
				continue
			}
			if p.BucketStore.IsAccessDeniedErr(err) {
				log.Warn("boot: cannot fetch file - ACCESS DENIED",
					slog.String("name", name),
					slog.String("file", file),
					slog.Any("err", err),
				)
				continue
			}
			log.Debug("boot: cannot fetch file",
				slog.String("name", name),
				slog.String("file", file),
				slog.Any("err", err),
			)
		}
	}

	log.Info("boot: all entries loaded", slog.Int("numEntries", len(p.memStore.entries)))

	return nil
}

func (p BucketEntryRepository) fetchEntries(ctx context.Context, file string) error {
	log := slogctx.FromCtx(ctx)
	t1 := time.Now()
	r, err := p.BucketStore.Get(ctx, file)
	log.Debug("fetched from s3",
		slog.String("file", file),
		slog.Int64("duration_ms", time.Since(t1).Milliseconds()),
	)
	if err != nil {
		return err
	}
	defer r.Close()

	var result []storedEntry
	err = json.NewDecoder(r).Decode(&result)
	if err != nil {
		return err
	}

	// repository.storedEntry{repository.storedEntry{Oid:"673f0b9c2d9a23bffdc4a2cb",
	// Type:"sgv", SgvMgdl:107, Direction:"Flat", Device:"nightscout-librelink-up",
	// Time:time.Date(2024, time.November, 21, 10, 29, 48, 0, time.UTC),
	// CreatedTime:time.Date(2024, time.November, 21, 12, 18, 24, 942444000, time.UTC)}}

	p.memStore.entriesLock.Lock()
	defer p.memStore.entriesLock.Unlock()
	p.memStore.deviceNamesLock.Lock()
	defer p.memStore.deviceNamesLock.Unlock()
	for _, e := range result {
		deviceID, ok := p.memStore.deviceIDsByName[e.Device]
		if !ok {
			deviceID = len(p.memStore.deviceIDsByName)
			p.memStore.deviceNames = append(p.memStore.deviceNames, e.Device)
			p.memStore.deviceIDsByName[e.Device] = deviceID
		}
		p.memStore.entries = append(p.memStore.entries, memEntry{
			EventTime:   e.Time,
			CreatedTime: e.CreatedTime,
			Oid:         e.Oid,
			Type:        e.Type,
			Trend:       e.Direction,
			SgvMgdl:     e.SgvMgdl,
			DeviceID:    deviceID,
		})
	}
	return nil
}

func (p BucketEntryRepository) FetchEntryByOid(ctx context.Context, oid string) (*models.Entry, error) {

	for i := len(p.memStore.entries) - 1; i >= 0; i-- {
		e := p.memStore.entries[i]
		if e.Oid != oid {
			continue
		}
		return &models.Entry{
			Oid:         e.Oid,
			Type:        e.Type,
			SgvMgdl:     e.SgvMgdl,
			Direction:   e.Trend,
			Device:      p.memStore.deviceNames[e.DeviceID],
			Time:        e.EventTime,
			CreatedTime: e.CreatedTime,
		}, nil
	}
	return nil, models.ErrNotFound
}

func (p BucketEntryRepository) FetchLatestSgvEntry(ctx context.Context) (*models.Entry, error) {

	// nb (unexpected?) future entries are excluded
	now := time.Now()
	for i := len(p.memStore.entries) - 1; i >= 0; i-- {
		e := p.memStore.entries[i]
		if e.Type != "sgv" {
			continue
		}
		if e.EventTime.After(now) {
			continue
		}
		return &models.Entry{
			Oid:         e.Oid,
			Type:        e.Type,
			SgvMgdl:     e.SgvMgdl,
			Direction:   e.Trend,
			Device:      p.memStore.deviceNames[e.DeviceID],
			Time:        e.EventTime,
			CreatedTime: e.CreatedTime,
		}, nil
	}

	return nil, models.ErrNotFound
}

func (p BucketEntryRepository) FetchLatestEntries(ctx context.Context, maxEntries int) ([]models.Entry, error) {

	// nb (unexpected?) future entries are excluded
	now := time.Now()
	var entries []models.Entry
	for i := len(p.memStore.entries) - 1; i >= 0; i-- {
		e := p.memStore.entries[i]

		if e.EventTime.After(now) {
			continue
		}
		entries = append(entries, models.Entry{
			Oid:         e.Oid,
			Type:        e.Type,
			SgvMgdl:     e.SgvMgdl,
			Direction:   e.Trend,
			Device:      p.memStore.deviceNames[e.DeviceID],
			Time:        e.EventTime,
			CreatedTime: e.CreatedTime,
		})
		if len(entries) == maxEntries {
			break
		}
	}
	return entries, nil
}

type storedEntry struct {
	Time        time.Time `json:"dateString"`
	CreatedTime time.Time `json:"sysTime"`
	Oid         string    `json:"_id"`
	Type        string    `json:"type"`
	Direction   string    `json:"direction"`
	Device      string    `json:"device"`
	SgvMgdl     int       `json:"sgv"`
}

// TODO: Events in the far future should end up in day file?

// syncToBucket will update any bucket objects that have been updated recently.
//
// Note currentTime arg is passed to avoid race condition around time boundaries.
// The year/month/day storage system is designed so we mostly update a single file,
// and at startup read four files (previous year, this year, this month, today).
// Data older than the previous year can be fetched on demand (?)
// They are designed such that reading those files will not have any duplicate/overlapping entries
func (p BucketEntryRepository) syncToBucket(ctx context.Context, currentTime time.Time) {
	log := slogctx.FromCtx(ctx)
	log.Debug("syncing",
		slog.Time("time", currentTime),
		slog.Bool("dirtyDay", p.memStore.dirtyDay),
		slog.Bool("dirtyMonth", p.memStore.dirtyMonth),
		slog.Any("dirtyYears", p.memStore.dirtyYears),
	)

	p.memStore.dirtyLock.Lock()
	defer p.memStore.dirtyLock.Unlock()
	p.syncDayToBucket(ctx, currentTime)
	p.memStore.dirtyDay = false
	p.syncMonthToBucket(ctx, currentTime)
	p.memStore.dirtyMonth = false
	p.syncYearsToBucket(ctx, currentTime)
	clear(p.memStore.dirtyYears)
}

// syncDayToBucket updates the day file in the object store.
// day files contain data for the current day
func (p BucketEntryRepository) syncDayToBucket(ctx context.Context, currentTime time.Time) {
	log := slogctx.FromCtx(ctx)
	if !p.memStore.dirtyDay {
		return
	}
	log.Debug("syncing day",
		slog.Time("time", currentTime),
		slog.Bool("dirtyDay", p.memStore.dirtyDay),
	)

	var dayEntries []storedEntry
	startOfDay := time.Date(currentTime.Year(), currentTime.Month(), currentTime.Day(), 0, 0, 0, 0, time.UTC)

	// TODO:  p.memStore.entries is sorted: we can use binary search to find start of day & write everything after that.
	for _, entry := range p.memStore.entries {
		if entry.EventTime.Before(startOfDay) {
			continue
		}
		dayEntries = append(dayEntries, storedEntry{
			Oid:         entry.Oid,
			Type:        entry.Type,
			SgvMgdl:     entry.SgvMgdl,
			Direction:   entry.Trend,
			Device:      p.memStore.deviceNames[entry.DeviceID],
			Time:        entry.EventTime,
			CreatedTime: entry.CreatedTime,
		})
	}

	name := fmt.Sprintf("ns-day/%s.json", currentTime.Format("2006-01-02"))
	p.writeEntriesToBucket(ctx, name, dayEntries)
}

func (p BucketEntryRepository) writeEntriesToBucket(ctx context.Context, name string, storedEntries []storedEntry) {
	log := slogctx.FromCtx(ctx)
	b, err := json.Marshal(storedEntries)
	if err != nil {
		log.Warn("cannot marshal entries", slog.String("name", name), slog.Any("err", err))
		return
	}

	r := bytes.NewReader(b)
	err = p.BucketStore.Upload(ctx, name, r)
	if err != nil {
		log.Warn("cannot upload entries", slog.String("name", name), slog.Any("err", err))
		return
	}
	slog.Debug("uploaded entries",
		slog.String("name", name),
		slog.Int("byteSize", len(b)),
		slog.Int("numEntries", len(storedEntries)),
	)
}

// syncMonthToBucket updates a month file in the object store.
// month files contain data for the current month, excluding today
func (p BucketEntryRepository) syncMonthToBucket(ctx context.Context, currentTime time.Time) {
	log := slogctx.FromCtx(ctx)
	if !p.memStore.dirtyMonth {
		return
	}
	log.Debug("syncing month",
		slog.Time("time", currentTime),
		slog.Bool("dirtyMonth", p.memStore.dirtyMonth),
	)
	var monthEntries []storedEntry
	startOfMonth := time.Date(currentTime.Year(), currentTime.Month(), 1, 0, 0, 0, 0, time.UTC)
	startOfDay := time.Date(currentTime.Year(), currentTime.Month(), currentTime.Day(), 0, 0, 0, 0, time.UTC)
	for _, entry := range p.memStore.entries {
		if entry.EventTime.Before(startOfMonth) {
			continue
		}
		// month files do not include today's data
		if entry.EventTime.After(startOfDay) {
			continue
		}
		monthEntries = append(monthEntries, storedEntry{
			Oid:         entry.Oid,
			Type:        entry.Type,
			SgvMgdl:     entry.SgvMgdl,
			Direction:   entry.Trend,
			Device:      p.memStore.deviceNames[entry.DeviceID],
			Time:        entry.EventTime,
			CreatedTime: entry.CreatedTime,
		})
	}
	name := fmt.Sprintf("ns-month/%s.json", currentTime.Format("2006-01"))
	p.writeEntriesToBucket(ctx, name, monthEntries)
}

// syncMonthToBucket updates a year file in the object store.
// previous year files contain all data for that year.
// the current year-file contains data for the current year, excluding this month.
func (p BucketEntryRepository) syncYearsToBucket(ctx context.Context, currentTime time.Time) {
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
	yearsEntries := make(map[int][]storedEntry)
	startOfYear := time.Date(currentTime.Year(), time.January, 1, 0, 0, 0, 0, time.UTC)
	startOfMonth := time.Date(currentTime.Year(), currentTime.Month(), 1, 0, 0, 0, 0, time.UTC)
	for _, e := range p.memStore.entries {
		if e.EventTime.Before(startOfYear) {
			continue
		}
		// year files do not include data for current month
		if e.EventTime.After(startOfMonth) {
			continue
		}
		yearsEntries[e.EventTime.Year()] = append(yearsEntries[e.EventTime.Year()], storedEntry{
			Oid:         e.Oid,
			Type:        e.Type,
			SgvMgdl:     e.SgvMgdl,
			Direction:   e.Trend,
			Device:      p.memStore.deviceNames[e.DeviceID],
			Time:        e.EventTime,
			CreatedTime: e.CreatedTime,
		})
	}
	name := fmt.Sprintf("ns-year/%s.json", currentTime.Format("2006"))
	p.writeEntriesToBucket(ctx, name, yearsEntries[currentTime.Year()])
}

// CreateEntries supports adding new entries to the db.
func (p BucketEntryRepository) CreateEntries(ctx context.Context, entries []models.Entry) ([]models.Entry, error) {
	var modelEntries []models.Entry
	if len(entries) == 0 {
		return modelEntries, nil
	}
	log := slogctx.FromCtx(ctx)
	now := time.Now()

	// TODO de-dupe. We want to support bulk-import, maybe 250k entries (6mo),
	// so naive scan-all-existing-entries for each new memEntry may be bad
	if len(p.memStore.entries) > 0 {
		lastEntry := p.memStore.entries[len(p.memStore.entries)-1]
		// events within 10 seconds of each other should be checked for dupes
		lastEventTime := lastEntry.EventTime.Add(time.Second * 10)
		for _, entry := range entries {
			if !entry.Time.After(lastEventTime) {
				// potential dupe
				log.Info("potential dupe", slog.Time("evt", entry.Time), slog.Time("lastEvent", lastEventTime))
			}
		}
	}

	startOfMonth := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	startOfDay := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)

	p.memStore.entriesLock.Lock()
	defer p.memStore.entriesLock.Unlock()
	p.memStore.dirtyLock.Lock()
	defer p.memStore.dirtyLock.Unlock()
	var lastEventTime time.Time // last as in "furthest forward in time"
	if len(p.memStore.entries) > 0 {
		lastEventTime = p.memStore.entries[len(p.memStore.entries)-1].EventTime
	}

	p.memStore.deviceNamesLock.Lock()
	defer p.memStore.deviceNamesLock.Unlock()

	entriesNeedSorting := false
	for _, e := range entries {

		deviceID, ok := p.memStore.deviceIDsByName[e.Device]
		if !ok {
			deviceID = len(p.memStore.deviceIDsByName)
			p.memStore.deviceNames = append(p.memStore.deviceNames, e.Device)
			p.memStore.deviceIDsByName[e.Device] = deviceID
		}

		// Preserve oid on import.
		// May need to rethink if we generate our own "oid"s with different structure
		oid := e.Oid
		if oid == "" {
			oid = primitive.NewObjectIDFromTimestamp(now).Hex()
		}

		memEntry := memEntry{
			Oid:         oid,
			Type:        e.Type,
			SgvMgdl:     e.SgvMgdl,
			Trend:       e.Direction,
			DeviceID:    deviceID,
			EventTime:   e.Time,
			CreatedTime: now,
		}
		if memEntry.Type == "" {
			memEntry.Type = "sgv"
		}

		p.memStore.entries = append(p.memStore.entries, memEntry)

		if memEntry.EventTime.Before(lastEventTime) {
			entriesNeedSorting = true
		}

		if memEntry.EventTime.After(startOfDay) {
			if !p.memStore.dirtyDay {
				p.memStore.dirtyDay = true
				log.Info("marking day dirty", slog.Any("memEntry", memEntry))
			}
		} else if memEntry.EventTime.After(startOfMonth) {
			if !p.memStore.dirtyMonth {
				log.Info("marking month dirty", slog.Any("memEntry", memEntry))
				p.memStore.dirtyMonth = true
			}
		} else {
			_, ok := p.memStore.dirtyYears[memEntry.EventTime.Year()]
			if !ok {
				p.memStore.dirtyYears[memEntry.EventTime.Year()] = struct{}{}
				log.Info("marking year dirty", slog.Int("year", memEntry.EventTime.Year()), slog.Any("memEntry", memEntry))
			}
		}

		lastEventTime = memEntry.EventTime

		modelEntries = append(modelEntries, models.Entry{
			Oid:         memEntry.Oid,
			Type:        memEntry.Type,
			SgvMgdl:     e.SgvMgdl,
			Direction:   e.Direction,
			Device:      e.Device,
			Time:        e.Time,
			CreatedTime: now,
		})
	}
	log.Info("inserted entries", slog.Int("totalEntries", len(p.memStore.entries)), slog.Int("numInserted", len(entries)))

	if entriesNeedSorting {
		t1 := time.Now()
		slices.SortFunc(p.memStore.entries, func(a, b memEntry) int { return a.EventTime.Compare(b.EventTime) })
		log.Debug("entries sorted", slog.Int64("duration_us", time.Since(t1).Microseconds()))
	}

	if p.memStore.dirtyMonth || p.memStore.dirtyDay || len(p.memStore.dirtyYears) != 0 {
		syncContext := context.WithoutCancel(ctx)
		go p.syncToBucket(syncContext, now)
	}

	return modelEntries, nil
}
