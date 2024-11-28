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
	assert.Nil(t, fetchedEntry)
}

// TestFetchLatestSgvEntry tests fetching the latest SGV memEntry
func TestFetchLatestSgvEntry(t *testing.T) {
	mockStore := &MockBucketStore{}
	repo := NewBucketEntryRepository(mockStore)

	now := time.Now()
	repo.memStore.entries = []memEntry{
		{Oid: "older", Type: "sgv", SgvMgdl: 100, Trend: "Flat", DeviceID: 0, EventTime: now.Add(-time.Hour), CreatedTime: now},
		{Oid: "latest", Type: "sgv", SgvMgdl: 150, Trend: "Up", DeviceID: 0, EventTime: now.Add(-time.Minute), CreatedTime: now},
		{Oid: "non-sgv", Type: "mbg", SgvMgdl: 99, DeviceID: 0, EventTime: now.Add(-time.Minute), CreatedTime: now},
		{Oid: "future", Type: "sgv", SgvMgdl: 150, Trend: "Up", DeviceID: 0, EventTime: now.Add(time.Minute), CreatedTime: now},
	}

	fetchedEntry, err := repo.FetchLatestSgvEntry(contextWithSilentLogger())
	assert.NoError(t, err)
	assert.Equal(t, "latest", fetchedEntry.Oid)

	repo.memStore.entries = []memEntry{
		{Oid: "non-sgv", Type: "mbg", SgvMgdl: 99, DeviceID: 0, EventTime: now.Add(-time.Minute), CreatedTime: now},
	}
	fetchedEntry, err = repo.FetchLatestSgvEntry(contextWithSilentLogger())
	assert.Error(t, err)
}

// TestFetchLatestEntries tests fetching the latest entries
func TestFetchLatestEntries(t *testing.T) {
	mockStore := &MockBucketStore{}
	repo := NewBucketEntryRepository(mockStore)

	now := time.Now()
	repo.memStore.entries = []memEntry{
		{Oid: "oid1", Type: "sgv", SgvMgdl: 100, Trend: "Flat", DeviceID: 0, EventTime: now.Add(-time.Hour), CreatedTime: now},
		{Oid: "oid2", Type: "sgv", SgvMgdl: 150, Trend: "Up", DeviceID: 0, EventTime: now.Add(-time.Minute), CreatedTime: now},
		{Oid: "future", Type: "sgv", SgvMgdl: 150, Trend: "Up", DeviceID: 0, EventTime: now.Add(time.Minute), CreatedTime: now},
	}

	entries, err := repo.FetchLatestEntries(contextWithSilentLogger(), 1)
	assert.NoError(t, err)
	assert.Len(t, entries, 1)
	assert.Equal(t, "oid2", entries[0].Oid)
}

// TestCreateEntries tests the creation of new entries
func TestCreateEntries(t *testing.T) {
	mockStore := &MockBucketStore{}
	repo := NewBucketEntryRepository(mockStore)

	// ensure spawned goroutine can sync/upload
	mockStore.On("Upload", mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// calling with no entries does not error
	createdEntries, err := repo.CreateEntries(contextWithSilentLogger(), []models.Entry{})
	assert.NoError(t, err)
	assert.Equal(t, len(createdEntries), 0)

	entries := []models.Entry{
		{
			SgvMgdl:   100,
			Direction: "Flat",
			Device:    "test-device-1",
			Time:      time.Now().Add(-time.Second),
		},
		{
			Oid:       "old-oid",
			Type:      "mbg",
			SgvMgdl:   150,
			Direction: "Up",
			Device:    "test-device-2",
			Time:      time.Now().Add(-time.Minute),
		},
	}

	createdEntries, err = repo.CreateEntries(contextWithSilentLogger(), entries)

	// entries returned in the same order as they were passed
	assert.NoError(t, err)
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

// TestSyncToBucket tests the various day/month/year sync functions
func TestSyncToBucket(t *testing.T) {
	mockStore := &MockBucketStore{}
	repo := NewBucketEntryRepository(mockStore)
	repo.memStore.deviceNames = []string{"unknown", "device1", "device2", "device3", "device4"}

	now := time.Date(2024, 11, 28, 10, 0, 0, 0, time.UTC)
	sameDay := time.Date(2024, 11, 28, 0, 0, 0, 0, time.UTC)
	sameMonth := time.Date(2024, 11, 1, 0, 0, 0, 0, time.UTC)
	sameYear := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	lastYear := time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC)
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
