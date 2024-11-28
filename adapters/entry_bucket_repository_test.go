package repository

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	slogctx "github.com/veqryn/slog-context"

	"github.com/adamlounds/nightscout-go/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

var now = time.Date(2024, 11, 28, 10, 0, 0, 0, time.UTC)
var recent = time.Date(2024, 11, 28, 9, 30, 0, 0, time.UTC)
var future = time.Date(2024, 11, 28, 11, 0, 0, 0, time.UTC)
var sameDay = time.Date(2024, 11, 28, 0, 0, 0, 0, time.UTC)
var sameMonth = time.Date(2024, 11, 1, 0, 0, 0, 0, time.UTC)
var sameYear = time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
var lastYear = time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC)

var nonSgvEntry = memEntry{Oid: "non-sgv", Type: "mbg", SgvMgdl: 98, DeviceID: 0, EventTime: recent, CreatedTime: now}
var recentEntry = memEntry{Oid: "latest", Type: "sgv", SgvMgdl: 98, Trend: "DoubleUp", DeviceID: 0, EventTime: recent, CreatedTime: now}
var futureEntry = memEntry{Oid: "future", Type: "sgv", SgvMgdl: 99, Trend: "FortyFiveUp", DeviceID: 0, EventTime: future, CreatedTime: now}
var sameDayEntry = memEntry{Oid: "older", Type: "sgv", SgvMgdl: 100, Trend: "Flat", DeviceID: 0, EventTime: sameDay, CreatedTime: now}
var sameMonthEntry = memEntry{Oid: "samemonth", Type: "sgv", SgvMgdl: 101, Trend: "SingleUp", DeviceID: 2, EventTime: sameMonth, CreatedTime: now}
var sameYearEntry = memEntry{Oid: "sameyear", Type: "sgv", SgvMgdl: 102, Trend: "Flat", DeviceID: 1, EventTime: sameYear, CreatedTime: now}
var lastYearEntry = memEntry{Oid: "lastyear", Type: "sgv", SgvMgdl: 103, Trend: "SingleDown", DeviceID: 0, EventTime: lastYear, CreatedTime: now}

// MockBucketStore is a mock implementation of the BucketStore interface
type MockBucketStore struct {
	mock.Mock
}

func (m *MockBucketStore) Get(ctx context.Context, name string) (io.ReadCloser, error) {
	args := m.Called(ctx, name)
	return args.Get(0).(io.ReadCloser), args.Error(1)
}

func (m *MockBucketStore) Upload(ctx context.Context, name string, r io.Reader) error {
	args := m.Called(ctx, name, r)
	return args.Error(0)
}

func (m *MockBucketStore) IsObjNotFoundErr(err error) bool {
	return err != nil && err.Error() == "not found"
}

func (m *MockBucketStore) IsAccessDeniedErr(err error) bool {
	return err != nil && err.Error() == "access denied"
}

func contextWithSilentLogger() context.Context {
	return slogctx.NewCtx(context.Background(), slog.New(slog.NewTextHandler(io.Discard, nil)))
}

// TestNewBucketEntryRepository tests the creation of a new BucketEntryRepository
func TestNewBucketEntryRepository(t *testing.T) {
	mockStore := &MockBucketStore{}
	repo := NewBucketEntryRepository(mockStore)

	assert.NotNil(t, repo)
	assert.Equal(t, mockStore, repo.BucketStore)
	assert.NotNil(t, repo.memStore)
}

func TestFetchEntries(t *testing.T) {
	mockStore := &MockBucketStore{}
	repo := NewBucketEntryRepository(mockStore)
	dayEntries := `[{"dateString":"2024-11-27T11:50:21.723Z","sysTime":"2024-11-27T11:56:16.158187Z","_id":"674708e0575df739a9711a40","type":"sgv","direction":"Flat","device":"G6 Native / G5 Native","sgv":105}]`
	mockStore.On("Get", mock.Anything, "ns-day/2024-11-27").Return(io.NopCloser(strings.NewReader(dayEntries)), nil)

	err := repo.fetchEntries(contextWithSilentLogger(), "ns-day/2024-11-27")

	mockStore.AssertExpectations(t)
	memEntries := repo.memStore.entries
	assert.NoError(t, err)
	assert.Len(t, memEntries, 1)
	assert.Len(t, repo.memStore.deviceNames, 2) // device Name has been detected & added to id list
	assert.Equal(t, "sgv", memEntries[0].Type)
	assert.Equal(t, 105, memEntries[0].SgvMgdl)
}

// TestFetchEntryByOid tests fetching an memEntry by Oid
func TestFetchEntryByOid(t *testing.T) {
	mockStore := &MockBucketStore{}
	repo := NewBucketEntryRepository(mockStore)

	// Prepare test data
	e := models.Entry{
		Oid:         "test-oid",
		Type:        "sgv",
		SgvMgdl:     100,
		Direction:   "Flat",
		Device:      "test-device",
		Time:        time.Now(),
		CreatedTime: time.Now(),
	}
	repo.memStore.entries = []memEntry{
		{
			Oid: "other-oid",
		},
		{
			Oid:         e.Oid,
			Type:        e.Type,
			SgvMgdl:     e.SgvMgdl,
			Trend:       e.Direction,
			DeviceID:    0,
			EventTime:   e.Time,
			CreatedTime: e.CreatedTime,
		},
		{
			Oid: "other-oid-2",
		},
	}

	fetchedEntry, err := repo.FetchEntryByOid(contextWithSilentLogger(), e.Oid)
	assert.NoError(t, err)
	assert.Equal(t, e.Oid, fetchedEntry.Oid)

	nonExistentOid := "non-existent-oid"
	fetchedEntry, err = repo.FetchEntryByOid(contextWithSilentLogger(), nonExistentOid)
	assert.Error(t, err)
	assert.ErrorIs(t, err, models.ErrNotFound)
	assert.Nil(t, fetchedEntry)
}

// TestFetchLatestSgvEntry tests fetching the latest SGV memEntry
func TestFetchLatestSgvEntry(t *testing.T) {
	mockStore := &MockBucketStore{}
	repo := NewBucketEntryRepository(mockStore)

	repo.memStore.entries = []memEntry{
		sameDayEntry,
		recentEntry,
		{Oid: "non-sgv", Type: "mbg", SgvMgdl: 99, DeviceID: 0, EventTime: recent, CreatedTime: now},
		futureEntry,
	}

	fetchedEntry, err := repo.FetchLatestSgvEntry(contextWithSilentLogger(), now)
	assert.NoError(t, err)
	assert.Equal(t, "latest", fetchedEntry.Oid)

	repo.memStore.entries = []memEntry{
		{Oid: "non-sgv", Type: "mbg", SgvMgdl: 99, DeviceID: 0, EventTime: recent, CreatedTime: now},
	}
	fetchedEntry, err = repo.FetchLatestSgvEntry(contextWithSilentLogger(), now)
	assert.Error(t, err)
}

func TestFetchLatestEntries(t *testing.T) {
	mockStore := &MockBucketStore{}
	repo := NewBucketEntryRepository(mockStore)

	repo.memStore.entries = []memEntry{
		{Oid: "oid1", Type: "sgv", SgvMgdl: 100, Trend: "Flat", DeviceID: 0, EventTime: sameDay, CreatedTime: now},
		{Oid: "oid2", Type: "sgv", SgvMgdl: 150, Trend: "Up", DeviceID: 0, EventTime: recent, CreatedTime: now},
		{Oid: "future", Type: "sgv", SgvMgdl: 150, Trend: "Up", DeviceID: 0, EventTime: future, CreatedTime: now},
	}

	entries, err := repo.FetchLatestEntries(contextWithSilentLogger(), now, 1)
	assert.NoError(t, err)
	assert.Len(t, entries, 1)
	assert.Equal(t, "oid2", entries[0].Oid)
}

func TestAddEntriesToMemStore(t *testing.T) {
	mockStore := &MockBucketStore{}
	repo := NewBucketEntryRepository(mockStore)

	// calling with no entries does not error
	createdEntries := repo.addEntriesToMemStore(contextWithSilentLogger(), now, []models.Entry{})
	assert.Equal(t, len(createdEntries), 0)

	entries := []models.Entry{
		{
			SgvMgdl:   100,
			Direction: "Flat",
			Device:    "test-device-1",
			Time:      recent,
		},
		{
			Oid:       "old-oid",
			Type:      "mbg",
			SgvMgdl:   150,
			Direction: "Up",
			Device:    "test-device-2",
			Time:      sameDay,
		},
	}

	createdEntries = repo.addEntriesToMemStore(contextWithSilentLogger(), now, entries)

	// entries returned in the same order as they were passed
	assert.Equal(t, len(entries), len(createdEntries))
	assert.NotEqual(t, createdEntries[0].Oid, "", "entry is assigned an Oid")
	assert.Equal(t, createdEntries[1].Oid, "old-oid")
	newOid := createdEntries[0].Oid

	// memory store is kept sorted by entry time
	assert.Len(t, repo.memStore.entries, 2)
	assert.Equal(t, repo.memStore.entries[0].Oid, "old-oid")
	assert.Equal(t, repo.memStore.entries[1].Oid, newOid, "memory store uses generated oid")
	assert.True(t, repo.memStore.entries[1].EventTime.After(repo.memStore.entries[0].EventTime), "memory store keeps entries sorted by time")
	assert.Equal(t, repo.memStore.entries[1].Type, "sgv", "unknown Type assumed to be sgv")
}

func TestAddEntriesToMemStoreDirtyDetection(t *testing.T) {
	mockStore := &MockBucketStore{}
	repo := NewBucketEntryRepository(mockStore)
	repo.memStore.entries = []memEntry{
		{Oid: "older", Type: "sgv", SgvMgdl: 100, Trend: "Flat", DeviceID: 0, EventTime: sameDay, CreatedTime: now},
	}

	// calling with no entries leaves everything clean
	createdEntries := repo.addEntriesToMemStore(contextWithSilentLogger(), now, []models.Entry{})
	assert.Equal(t, len(createdEntries), 0)
	assert.False(t, repo.memStore.dirtyDay)
	assert.False(t, repo.memStore.dirtyMonth)
	assert.Zero(t, len(repo.memStore.dirtyYears))

	todayEntries := []models.Entry{
		{SgvMgdl: 120, Time: recent},
		{SgvMgdl: 121, Time: future},
		{SgvMgdl: 122, Time: sameDay},
	}

	createdEntries = repo.addEntriesToMemStore(contextWithSilentLogger(), now, todayEntries)

	assert.Equal(t, len(createdEntries), 3)
	assert.Len(t, repo.memStore.entries, 4) // including the original entry
	assert.True(t, repo.memStore.dirtyDay)
	assert.False(t, repo.memStore.dirtyMonth)
	assert.Zero(t, len(repo.memStore.dirtyYears))

	repo.memStore.dirtyDay = false
	thisMonthEntries := []models.Entry{
		{SgvMgdl: 130, Time: sameDay.Add(-time.Second)},
		{SgvMgdl: 131, Time: sameMonth},
	}

	createdEntries = repo.addEntriesToMemStore(contextWithSilentLogger(), now, thisMonthEntries)

	assert.Equal(t, len(createdEntries), 2)
	assert.Len(t, repo.memStore.entries, 6)
	assert.False(t, repo.memStore.dirtyDay)
	assert.True(t, repo.memStore.dirtyMonth)
	assert.Zero(t, len(repo.memStore.dirtyYears))

	repo.memStore.dirtyMonth = false
	thisYearEntries := []models.Entry{
		{SgvMgdl: 140, Time: sameMonth.Add(-time.Second)},
		{SgvMgdl: 141, Time: sameYear},
		{SgvMgdl: 142, Time: lastYear},
	}

	createdEntries = repo.addEntriesToMemStore(contextWithSilentLogger(), now, thisYearEntries)

	assert.Equal(t, len(createdEntries), 3)
	assert.Len(t, repo.memStore.entries, 9)
	assert.False(t, repo.memStore.dirtyDay)
	assert.False(t, repo.memStore.dirtyMonth)
	assert.Exactly(t, 2, len(repo.memStore.dirtyYears))
	assert.Contains(t, repo.memStore.dirtyYears, 2024)
	assert.Contains(t, repo.memStore.dirtyYears, 2023)
}

// TestSyncToBucket tests the various day/month/year sync functions
func TestSyncToBucket(t *testing.T) {
	mockStore := &MockBucketStore{}
	repo := NewBucketEntryRepository(mockStore)
	repo.memStore.deviceNames = []string{"unknown", "device1", "device2", "device3", "device4"}

	repo.memStore.entries = []memEntry{
		{Oid: "sameday", Type: "sgv", SgvMgdl: 100, Trend: "DoubleUp", DeviceID: 3, EventTime: sameDay, CreatedTime: now},
		{Oid: "samemonth", Type: "sgv", SgvMgdl: 101, Trend: "SingleUp", DeviceID: 2, EventTime: sameMonth, CreatedTime: now},
		{Oid: "sameyear", Type: "sgv", SgvMgdl: 102, Trend: "Flat", DeviceID: 1, EventTime: sameYear, CreatedTime: now},
		{Oid: "lastyear", Type: "sgv", SgvMgdl: 103, Trend: "SingleDown", DeviceID: 0, EventTime: lastYear, CreatedTime: now},
	}
	repo.memStore.dirtyDay = true
	repo.memStore.dirtyMonth = true
	repo.memStore.dirtyYears = map[int]struct{}{
		2024: {},
		//2023: {},
	}

	// day files contain entries from today
	thisDayMatcher := mock.MatchedBy(func(r io.ReadSeeker) bool {
		json, _ := io.ReadAll(r)
		r.Seek(0, io.SeekStart) //nolint:errcheck
		expectedJSON := `[{"dateString":"2024-11-28T00:00:00Z","sysTime":"2024-11-28T10:00:00Z","_id":"sameday","type":"sgv","direction":"DoubleUp","device":"device3","sgv":100}]`
		return string(json) == expectedJSON
	})
	mockStore.On("Upload", mock.Anything, "ns-day/2024-11-28.json", thisDayMatcher).Return(nil).Once()

	// month files contain entries from this month, excluding today
	thisMonthMatcher := mock.MatchedBy(func(r io.ReadSeeker) bool {
		json, _ := io.ReadAll(r)
		r.Seek(0, io.SeekStart) //nolint:errcheck
		expectedJSON := `[{"dateString":"2024-11-01T00:00:00Z","sysTime":"2024-11-28T10:00:00Z","_id":"samemonth","type":"sgv","direction":"SingleUp","device":"device2","sgv":101}]`
		return string(json) == expectedJSON
	})
	mockStore.On("Upload", mock.Anything, "ns-month/2024-11.json", thisMonthMatcher).Return(nil)

	// year files contain entries from this year, excluding the current month
	thisYearMatcher := mock.MatchedBy(func(r io.ReadSeeker) bool {
		json, _ := io.ReadAll(r)
		r.Seek(0, io.SeekStart) //nolint:errcheck
		expectedJSON := `[{"dateString":"2024-01-01T00:00:00Z","sysTime":"2024-11-28T10:00:00Z","_id":"sameyear","type":"sgv","direction":"Flat","device":"device1","sgv":102}]`
		return string(json) == expectedJSON
	})
	mockStore.On("Upload", mock.Anything, "ns-year/2024.json", thisYearMatcher).Return(nil)

	// TODO implement writing of other years too
	//lastYearMatcher := mock.MatchedBy(func(r io.ReadSeeker) bool {
	//	json, _ := io.ReadAll(r)
	//  r.Seek(0, io.SeekStart)
	//	expectedJSON := `[{"dateString":"2023-01-01T00:00:00Z","sysTime":"2024-11-28T10:00:00Z","_id":"lastyear","type":"sgv","direction":"SingleDown","device":"device1","sgv":103}]`
	//	fmt.Println(string(json))
	//	return string(json) == expectedJSON
	//})
	//mockStore.On("Upload", mock.Anything, "ns-year/2023.json", lastYearMatcher).Return(nil)

	repo.syncToBucket(contextWithSilentLogger(), now)

	mockStore.AssertExpectations(t)
}
