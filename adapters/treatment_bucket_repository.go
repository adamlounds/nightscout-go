package repository

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"time"

	"github.com/adamlounds/nightscout-go/models"
	slogctx "github.com/veqryn/slog-context"
	"go.mongodb.org/mongo-driver/bson/primitive"
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

type storedTreatment struct {
	Time        time.Time `json:"dateString"`
	CreatedTime time.Time `json:"sysTime"`
	Type        string    `json:"type"`
	Oid         string    `json:"_id"`
	fields      map[string]interface{}

	//// Common treatment fields
	//Insulin   float64 `json:"insulin,omitempty"`
	//Carbs     int     `json:"carbs,omitempty"`
	//Duration  int     `json:"duration,omitempty"`
	//Notes     string  `json:"notes,omitempty"`
	//EnteredBy string  `json:"enteredBy,omitempty"`
	//// Additional fields can be added as needed
}

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
		err := p.fetchTreatments(ctx, file)
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

func (p BucketTreatmentRepository) fetchTreatments(ctx context.Context, file string) error {
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
		p.memTreatmentStore.treatments = append(p.memTreatmentStore.treatments, memTreatment{
			Time: t.Time,
			Oid:  t.Oid,
			Type: t.Type,
			// ... add other fields as needed
		})
	}
	return nil
}

func (p BucketTreatmentRepository) FetchTreatmentByOid(ctx context.Context, oid string) (*models.Treatment, error) {
	for i := len(p.memTreatmentStore.treatments) - 1; i >= 0; i-- {
		t := p.memTreatmentStore.treatments[i]
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

func (p BucketTreatmentRepository) FetchLatestTreatments(ctx context.Context, maxTime time.Time, maxTreatments int) ([]models.Treatment, error) {
	var treatments []models.Treatment
	for i := len(p.memTreatmentStore.treatments) - 1; i >= 0; i-- {
		t := p.memTreatmentStore.treatments[i]

		if t.Time.After(maxTime) {
			continue
		}
		//if t.Type == "BG Check" {
		//	treatments = append(treatments, models.BGCheckTreatment{
		//		ID:        t.Oid,
		//		Time:      t.Time,
		//		BGMgdl:    123,
		//		BGSource:  "Sensor",
		//		Carbs:     nil,
		//		EnteredBy: nil,
		//		Insulin:   nil,
		//		Notes:     nil,
		//	})
		//}
		if len(treatments) == maxTreatments {
			break
		}
	}
	return treatments, nil
}

// syncToBucket will update any bucket objects that have been updated recently.
func (p BucketTreatmentRepository) syncToBucket(ctx context.Context, currentTime time.Time) {
	//log := slogctx.FromCtx(ctx)
	//log.Debug("syncing",
	//	slog.Time("time", currentTime),
	//	slog.Bool("dirtyDay", p.memTreatmentStore.dirtyDay),
	//	slog.Bool("dirtyMonth", p.memTreatmentStore.dirtyMonth),
	//	slog.Any("dirtyYears", p.memTreatmentStore.dirtyYears),
	//)
	//
	//p.memTreatmentStore.dirtyLock.Lock()
	//defer p.memTreatmentStore.dirtyLock.Unlock()
	//p.syncDayToBucket(ctx, currentTime)
	//p.memTreatmentStore.dirtyDay = false
	//p.syncMonthToBucket(ctx, currentTime)
	//p.memTreatmentStore.dirtyMonth = false
	//p.syncYearsToBucket(ctx, currentTime)
	//clear(p.memTreatmentStore.dirtyYears)
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
		dayTreatments = append(dayTreatments, storedTreatment{
			Oid:  treatment.Oid,
			Type: treatment.Type,
			Time: treatment.Time,
			// ... map other treatment fields
		})
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
		monthTreatments = append(monthTreatments, storedTreatment{
			Oid:  treatment.Oid,
			Type: treatment.Type,
			Time: treatment.Time,
			// ... map other treatment fields
		})
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

	for _, t := range p.memTreatmentStore.treatments {
		if t.Time.Before(startOfYear) {
			continue
		}
		if !t.Time.Before(startOfMonth) {
			continue
		}
		yearsTreatments[t.Time.Year()] = append(yearsTreatments[t.Time.Year()], storedTreatment{
			Oid:  t.Oid,
			Type: t.Type,
			Time: t.Time,
			// ... map other treatment fields
		})
	}
	name := fmt.Sprintf("ns-year/%d-treatments.json", currentTime.Year())
	p.writeTreatmentsToBucket(ctx, name, yearsTreatments[currentTime.Year()])
}

func (p BucketTreatmentRepository) CreateTreatments(ctx context.Context, treatments []models.Treatment) []models.Treatment {
	now := time.Now()
	createdTreatments := p.addTreatmentsToMemStore(ctx, now, treatments)

	//if p.memTreatmentStore.dirtyMonth || p.memTreatmentStore.dirtyDay || len(p.memTreatmentStore.dirtyYears) != 0 {
	//	syncContext := context.WithoutCancel(ctx)
	//	go p.syncToBucket(syncContext, now)
	//}
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
