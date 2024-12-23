package repository

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/adamlounds/nightscout-go/models"
	slogctx "github.com/veqryn/slog-context"
	"go.mongodb.org/mongo-driver/bson/primitive"
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
// Potential issue/weirdness: imported entries will have created_time based on
// the original/imported oid, not when this system first saw them.
type memTreatment struct {
	Time   time.Time
	Oid    string
	Type   string
	fields map[string]interface{}
}

func (t *memTreatment) IsAfter(time time.Time) bool {
	return t.Time.After(time)
}

type storedTreatment map[string]interface{}

type memTreatmentStore struct {
	dirtyYears     map[int]struct{} // new memEntry outside of this month: update year file
	treatments     []memTreatment
	treatmentsLock sync.Mutex
	dirtyLock      sync.Mutex
	dirtyDay       bool // new memTreatment today = update day file
	dirtyMonth     bool // new memTreatment this month (but not today): update month
}

type BucketTreatmentRepository struct {
	BucketStore       BucketStoreInterface
	memTreatmentStore *memTreatmentStore
}

func NewBucketTreatmentRepository(bs BucketStoreInterface) *BucketTreatmentRepository {
	m := &memTreatmentStore{
		dirtyYears: make(map[int]struct{}),
	}
	return &BucketTreatmentRepository{bs, m}
}

// Boot fetches common data into memory, typically at server startup
func (p BucketTreatmentRepository) Boot(ctx context.Context) error {
	log := slogctx.FromCtx(ctx)

	// loading files in order means we don't have to sort afterwards.
	now := time.Now()
	entryFiles := []string{
		fmt.Sprintf("ns-year/%d-treatments.json", now.Year()-1),            // last year
		fmt.Sprintf("ns-year/%d-treatments.json", now.Year()),              // year to date excl month
		fmt.Sprintf("ns-month/%s-treatments.json", now.Format("2006-01")),  // month to date excl today
		fmt.Sprintf("ns-day/%s-treatments.json", now.Format("2006-01-02")), // today
	}

	for _, file := range entryFiles {
		err := p.loadTreatments(ctx, file)
		if err != nil {
			if p.BucketStore.IsObjNotFoundErr(err) {
				log.Debug("boot: cannot find file (not written yet?)",
					slog.String("file", file),
				)
				continue
			}
			if p.BucketStore.IsAccessDeniedErr(err) {
				log.Warn("boot: cannot fetch file - ACCESS DENIED",
					slog.String("file", file),
					slog.Any("err", err),
				)
				continue
			}
			log.Debug("boot: cannot fetch file",
				slog.String("file", file),
				slog.Any("err", err),
			)
		}
	}

	var mostRecentTime time.Time
	if len(p.memTreatmentStore.treatments) > 0 {
		mostRecentTime = p.memTreatmentStore.treatments[len(p.memTreatmentStore.treatments)-1].Time
	}
	log.Info("boot: all entries loaded",
		slog.Int("numEntries", len(p.memTreatmentStore.treatments)),
		slog.Time("mostRecentTreatmentTime", mostRecentTime),
	)

	return nil
}

func (p BucketTreatmentRepository) loadTreatments(ctx context.Context, file string) error {
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

	var result []storedTreatment
	err = json.NewDecoder(r).Decode(&result)
	if err != nil {
		return err
	}

	p.memTreatmentStore.treatmentsLock.Lock()
	defer p.memTreatmentStore.treatmentsLock.Unlock()
	for _, t := range result {
		tTimeStr, ok := t["created_at"].(string)
		if !ok {
			log.Warn("loadTreatments: cannot find time", slog.Any("treatment", t))
			continue
		}
		tTime, err := time.Parse(time.RFC3339, tTimeStr)
		if err != nil {
			log.Warn("loadTreatments: cannot parse time", slog.Any("time", t["time"]))
			continue
		}
		tType, ok := t["eventType"].(string)
		if !ok {
			log.Warn("loadTreatments: cannot find type", slog.Any("treatment", t))
			continue
		}
		tOid, ok := t["_id"].(string)
		if !ok {
			log.Warn("loadTreatments: cannot find _id", slog.Any("treatment", t))
			continue
		}

		delete(t, "_id")
		delete(t, "created_at")
		delete(t, "eventType")

		p.memTreatmentStore.treatments = append(p.memTreatmentStore.treatments, memTreatment{
			Time:   tTime,
			Oid:    tOid,
			Type:   tType,
			fields: t,
		})
	}
	return nil
}

func (p BucketTreatmentRepository) FetchTreatmentByOid(ctx context.Context, oid string) (*models.Treatment, error) {
	memTreatments := p.memTreatmentStore.treatments

	for i := len(memTreatments) - 1; i >= 0; i-- {
		t := memTreatments[i]
		if t.Oid != oid {
			continue
		}

		return &models.Treatment{
			ID:     t.Oid,
			Time:   t.Time,
			Type:   t.Type,
			Fields: t.fields,
		}, nil
	}

	return nil, models.ErrNotFound
}

func (p BucketTreatmentRepository) DeleteTreatmentByOid(ctx context.Context, oid string) error {
	log := slogctx.FromCtx(ctx)
	memTreatments := p.memTreatmentStore.treatments

	for i := len(memTreatments) - 1; i >= 0; i-- {
		t := memTreatments[i]
		if t.Oid != oid {
			continue
		}

		p.memTreatmentStore.treatments = append(memTreatments[:i], memTreatments[i+1:]...)

		now := time.Now()
		startOfMonth := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
		startOfDay := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
		if !t.Time.Before(startOfDay) {
			if !p.memTreatmentStore.dirtyDay {
				log.Debug("marking day dirty", slog.Time("deletedTreatmentTime", t.Time))
				p.memTreatmentStore.dirtyDay = true
			}
		} else if !t.Time.Before(startOfMonth) {
			if !p.memTreatmentStore.dirtyMonth {
				log.Debug("marking month dirty", slog.Time("deletedTreatmentTime", t.Time))
				p.memTreatmentStore.dirtyMonth = true
			}
		} else {
			_, ok := p.memTreatmentStore.dirtyYears[t.Time.Year()]
			if !ok {
				log.Debug("marking year dirty", slog.Int("year", t.Time.Year()))
				p.memTreatmentStore.dirtyYears[t.Time.Year()] = struct{}{}
			}
		}

		// TODO mark things dirty, trigger save

		// something _must_ be dirty, so trigger sync
		syncContext := context.WithoutCancel(ctx)
		go p.syncToBucket(syncContext, now)

		return nil
	}

	return models.ErrNotFound
}

func (p BucketTreatmentRepository) UpdateTreatmentByOid(ctx context.Context, oid string, treatment *models.Treatment) error {
	log := slogctx.FromCtx(ctx)
	memTreatments := p.memTreatmentStore.treatments

	treatmentsNeedSorting := false
	for i := len(memTreatments) - 1; i >= 0; i-- {
		t := memTreatments[i]
		if t.Oid != oid {
			continue
		}

		now := time.Now()
		startOfMonth := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
		startOfDay := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)

		// if time has changed we probably need to do more cleanup
		if t.Time != treatment.Time {
			treatmentsNeedSorting = true
			p.markDirty(ctx, startOfMonth, startOfDay, t.Time)
			t.Time = treatment.Time
		}
		t.Type = treatment.Type
		t.fields = treatment.Fields

		delete(t.fields, "_id")
		delete(t.fields, "eventType")
		delete(t.fields, "eventTime")
		delete(t.fields, "created_at")

		memTreatments[i] = t

		p.markDirty(ctx, startOfMonth, startOfDay, t.Time)

		if treatmentsNeedSorting {
			t1 := time.Now()
			slices.SortFunc(p.memTreatmentStore.treatments, func(a, b memTreatment) int { return a.Time.Compare(b.Time) })
			log.Debug("treatments sorted", slog.Int64("duration_us", time.Since(t1).Microseconds()))
		}

		// assume a change was made: trigger sync
		syncContext := context.WithoutCancel(ctx)
		go p.syncToBucket(syncContext, now)

		return nil
	}

	return models.ErrNotFound
}
func (p BucketTreatmentRepository) markDirty(ctx context.Context, startOfMonth time.Time, startOfDay time.Time, t time.Time) {
	log := slogctx.FromCtx(ctx)
	if !t.Before(startOfDay) {
		if !p.memTreatmentStore.dirtyDay {
			log.Debug("marking day dirty", slog.Time("t", t))
			p.memTreatmentStore.dirtyDay = true
		}
	} else if !t.Before(startOfMonth) {
		if !p.memTreatmentStore.dirtyMonth {
			log.Debug("marking month dirty", slog.Time("t", t))
			p.memTreatmentStore.dirtyMonth = true
		}
	} else {
		_, ok := p.memTreatmentStore.dirtyYears[t.Year()]
		if !ok {
			log.Debug("marking year dirty", slog.Int("year", t.Year()), slog.Time("t", t))
			p.memTreatmentStore.dirtyYears[t.Year()] = struct{}{}
		}
	}
}

func (p BucketTreatmentRepository) FetchLatestTreatments(ctx context.Context, maxTime time.Time, maxTreatments int) ([]models.Treatment, error) {
	memTreatments := p.memTreatmentStore.treatments

	if len(memTreatments) < maxTreatments {
		maxTreatments = len(memTreatments)
	}
	treatments := make([]models.Treatment, 0, maxTreatments)

	for i := len(memTreatments) - 1; i >= 0; i-- {
		t := memTreatments[i]

		if t.Time.After(maxTime) {
			continue
		}
		treatments = append(treatments, models.Treatment{
			ID:     t.Oid,
			Time:   t.Time,
			Type:   t.Type,
			Fields: t.fields,
		})
		if len(treatments) == maxTreatments {
			break
		}
	}
	return treatments, nil
}

// syncToBucket will update any bucket objects that have been updated recently.
func (p BucketTreatmentRepository) syncToBucket(ctx context.Context, currentTime time.Time) {
	log := slogctx.FromCtx(ctx)
	log.Debug("syncing treatments",
		slog.Time("time", currentTime),
		slog.Bool("dirtyDay", p.memTreatmentStore.dirtyDay),
		slog.Bool("dirtyMonth", p.memTreatmentStore.dirtyMonth),
		slog.Any("dirtyYears", p.memTreatmentStore.dirtyYears),
	)

	p.memTreatmentStore.dirtyLock.Lock()
	defer p.memTreatmentStore.dirtyLock.Unlock()
	p.syncDayToBucket(ctx, currentTime)
	p.memTreatmentStore.dirtyDay = false
	p.syncMonthToBucket(ctx, currentTime)
	p.memTreatmentStore.dirtyMonth = false
	p.syncYearsToBucket(ctx, currentTime)
	clear(p.memTreatmentStore.dirtyYears)
}

func (p BucketTreatmentRepository) syncDayToBucket(ctx context.Context, currentTime time.Time) {
	log := slogctx.FromCtx(ctx)
	if !p.memTreatmentStore.dirtyDay {
		return
	}
	log.Debug("syncing day",
		slog.Time("time", currentTime),
		slog.Bool("dirtyDay", p.memTreatmentStore.dirtyDay),
	)

	var dayTreatments []storedTreatment
	startOfDay := time.Date(currentTime.Year(), currentTime.Month(), currentTime.Day(), 0, 0, 0, 0, time.UTC)

	for _, treatment := range p.memTreatmentStore.treatments {
		if treatment.Time.Before(startOfDay) {
			continue
		}
		st := storedTreatment{
			"_id":        treatment.Oid,
			"created_at": treatment.Time.Format(time.RFC3339),
			"eventType":  treatment.Type,
		}
		for k, v := range treatment.fields {
			st[k] = v
		}
		dayTreatments = append(dayTreatments, st)
	}

	name := fmt.Sprintf("ns-day/%s-treatments.json", currentTime.Format("2006-01-02"))
	p.writeTreatmentsToBucket(ctx, name, dayTreatments)
}

func (p BucketTreatmentRepository) writeTreatmentsToBucket(ctx context.Context, name string, storedTreatments []storedTreatment) {
	log := slogctx.FromCtx(ctx)
	b, err := json.Marshal(storedTreatments)
	if err != nil {
		log.Warn("cannot marshal treatments", slog.String("name", name), slog.Any("err", err))
		return
	}

	r := bytes.NewReader(b)
	err = p.BucketStore.Upload(ctx, name, r)
	if err != nil {
		log.Warn("cannot upload treatments", slog.String("name", name), slog.Any("err", err))
		return
	}
	log.Debug("uploaded treatments",
		slog.String("name", name),
		slog.Int("byteSize", len(b)),
		slog.Int("numTreatments", len(storedTreatments)),
	)
}

func (p BucketTreatmentRepository) syncMonthToBucket(ctx context.Context, currentTime time.Time) {
	log := slogctx.FromCtx(ctx)
	if !p.memTreatmentStore.dirtyMonth {
		return
	}
	log.Debug("syncing month",
		slog.Time("time", currentTime),
		slog.Bool("dirtyMonth", p.memTreatmentStore.dirtyMonth),
	)
	var monthTreatments []storedTreatment
	startOfMonth := time.Date(currentTime.Year(), currentTime.Month(), 1, 0, 0, 0, 0, time.UTC)
	startOfDay := time.Date(currentTime.Year(), currentTime.Month(), currentTime.Day(), 0, 0, 0, 0, time.UTC)

	for _, treatment := range p.memTreatmentStore.treatments {
		if treatment.Time.Before(startOfMonth) {
			continue
		}
		if !treatment.Time.Before(startOfDay) {
			continue
		}

		st := storedTreatment{
			"_id":        treatment.Oid,
			"created_at": treatment.Time.Format(time.RFC3339),
			"eventType":  treatment.Type,
		}
		for k, v := range treatment.fields {
			st[k] = v
		}
		monthTreatments = append(monthTreatments, st)
	}
	name := fmt.Sprintf("ns-month/%s-treatments.json", currentTime.Format("2006-01"))
	p.writeTreatmentsToBucket(ctx, name, monthTreatments)
}

func (p BucketTreatmentRepository) syncYearsToBucket(ctx context.Context, currentTime time.Time) {
	log := slogctx.FromCtx(ctx)
	if len(p.memTreatmentStore.dirtyYears) == 0 {
		return
	}
	log.Debug("syncing years",
		slog.Time("time", currentTime),
		slog.Any("dirtyYears", p.memTreatmentStore.dirtyYears),
	)

	yearsTreatments := make(map[int][]storedTreatment)
	startOfYear := time.Date(currentTime.Year(), time.January, 1, 0, 0, 0, 0, time.UTC)
	startOfMonth := time.Date(currentTime.Year(), currentTime.Month(), 1, 0, 0, 0, 0, time.UTC)

	for _, treatment := range p.memTreatmentStore.treatments {
		if treatment.Time.Before(startOfYear) {
			continue
		}
		if !treatment.Time.Before(startOfMonth) {
			continue
		}
		st := storedTreatment{
			"_id":        treatment.Oid,
			"created_at": treatment.Time.Format(time.RFC3339),
			"eventType":  treatment.Type,
		}
		for k, v := range treatment.fields {
			st[k] = v
		}
		yearsTreatments[treatment.Time.Year()] = append(yearsTreatments[treatment.Time.Year()], st)
	}
	name := fmt.Sprintf("ns-year/%d-treatments.json", currentTime.Year())
	p.writeTreatmentsToBucket(ctx, name, yearsTreatments[currentTime.Year()])
}

func (p BucketTreatmentRepository) CreateTreatments(ctx context.Context, treatments []models.Treatment) []models.Treatment {
	now := time.Now()
	createdTreatments := p.addTreatmentsToMemStore(ctx, now, treatments)

	if p.memTreatmentStore.dirtyMonth || p.memTreatmentStore.dirtyDay || len(p.memTreatmentStore.dirtyYears) != 0 {
		syncContext := context.WithoutCancel(ctx)
		go p.syncToBucket(syncContext, now)
	}
	return createdTreatments
}

func (p BucketTreatmentRepository) addTreatmentsToMemStore(ctx context.Context, now time.Time, treatments []models.Treatment) []models.Treatment {
	var modelTreatments []models.Treatment
	if len(treatments) == 0 {
		return modelTreatments
	}
	log := slogctx.FromCtx(ctx)

	if len(p.memTreatmentStore.treatments) > 0 {
		lastTreatment := p.memTreatmentStore.treatments[len(p.memTreatmentStore.treatments)-1]
		lastTreatmentTime := lastTreatment.Time.Add(time.Second * 10)
		for _, treatment := range treatments {
			if !treatment.Time.After(lastTreatmentTime) {
				log.Info("potential dupe", slog.Time("treatment", treatment.Time), slog.Time("lastTreatment", lastTreatmentTime))
			}
		}
	}

	startOfMonth := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	startOfDay := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)

	p.memTreatmentStore.treatmentsLock.Lock()
	defer p.memTreatmentStore.treatmentsLock.Unlock()
	p.memTreatmentStore.dirtyLock.Lock()
	defer p.memTreatmentStore.dirtyLock.Unlock()

	var lastTreatmentTime time.Time
	if len(p.memTreatmentStore.treatments) > 0 {
		lastTreatmentTime = p.memTreatmentStore.treatments[len(p.memTreatmentStore.treatments)-1].Time
	}

	treatmentsNeedSorting := false
	for _, t := range treatments {
		oid := t.ID
		if oid == "" {
			oid = primitive.NewObjectIDFromTimestamp(now).Hex()
		}

		memTreatment := memTreatment{
			Oid:    oid,
			Type:   t.Type,
			Time:   t.Time,
			fields: t.Fields,
		}
		delete(memTreatment.fields, "_id")
		delete(memTreatment.fields, "eventType")
		delete(memTreatment.fields, "eventTime")
		delete(memTreatment.fields, "created_at")

		p.memTreatmentStore.treatments = append(p.memTreatmentStore.treatments, memTreatment)

		if memTreatment.Time.Before(lastTreatmentTime) {
			treatmentsNeedSorting = true
		}

		if !memTreatment.Time.Before(startOfDay) {
			if !p.memTreatmentStore.dirtyDay {
				p.memTreatmentStore.dirtyDay = true
				log.Debug("marking day dirty", slog.Any("memTreatment", memTreatment))
			}
		} else if !memTreatment.Time.Before(startOfMonth) {
			if !p.memTreatmentStore.dirtyMonth {
				log.Debug("marking month dirty", slog.Any("memTreatment", memTreatment))
				p.memTreatmentStore.dirtyMonth = true
			}
		} else {
			_, ok := p.memTreatmentStore.dirtyYears[memTreatment.Time.Year()]
			if !ok {
				p.memTreatmentStore.dirtyYears[memTreatment.Time.Year()] = struct{}{}
				log.Debug("marking year dirty", slog.Int("year", memTreatment.Time.Year()), slog.Any("memTreatment", memTreatment))
			}
		}

		lastTreatmentTime = memTreatment.Time

		t.ID = oid
		modelTreatments = append(modelTreatments, t)
	}
	log.Info("inserted treatments", slog.Int("totalTreatments", len(p.memTreatmentStore.treatments)), slog.Int("numInserted", len(treatments)))

	if treatmentsNeedSorting {
		t1 := time.Now()
		slices.SortFunc(p.memTreatmentStore.treatments, func(a, b memTreatment) int { return a.Time.Compare(b.Time) })
		log.Debug("treatments sorted", slog.Int64("duration_us", time.Since(t1).Microseconds()))
	}

	return modelTreatments
}
