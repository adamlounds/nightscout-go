package controllers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	repository "github.com/adamlounds/nightscout-go/adapters"
	"github.com/adamlounds/nightscout-go/models"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/stretchr/testify/assert"
	slogctx "github.com/veqryn/slog-context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func contextWithSilentLogger() context.Context {
	return slogctx.NewCtx(context.Background(), slog.New(slog.NewTextHandler(io.Discard, nil)))
}

type mockNightscoutRepository struct {
	fetchAllEntriesFn func(ctx context.Context, nsCfg repository.NightscoutConfig) ([]models.Entry, error)
}

func (m mockNightscoutRepository) FetchAllEntries(ctx context.Context, nsCfg repository.NightscoutConfig) ([]models.Entry, error) {
	return m.fetchAllEntriesFn(ctx, nsCfg)
}

type mockEntryRepository struct {
	fetchByOidFn      func(ctx context.Context, oid string) (*models.Entry, error)
	fetchLatestFn     func(ctx context.Context, maxTime time.Time) (*models.Entry, error)
	fetchLatestListFn func(ctx context.Context, maxTime time.Time, maxEntries int) ([]models.Entry, error)
	fetchLatestSGVsFn func(ctx context.Context, maxTime time.Time, maxEntries int) ([]models.Entry, error)
	createEntriesFn   func(ctx context.Context, entries []models.Entry) []models.Entry
}

func (m mockEntryRepository) FetchEntryByOid(ctx context.Context, oid string) (*models.Entry, error) {
	return m.fetchByOidFn(ctx, oid)
}
func (m mockEntryRepository) FetchLatestSgvEntry(ctx context.Context, maxTime time.Time) (*models.Entry, error) {
	return m.fetchLatestFn(ctx, maxTime)
}
func (m mockEntryRepository) FetchLatestEntries(ctx context.Context, maxTime time.Time, maxEntries int) ([]models.Entry, error) {
	return m.fetchLatestListFn(ctx, maxTime, maxEntries)
}
func (m mockEntryRepository) FetchLatestSGVs(ctx context.Context, maxTime time.Time, maxEntries int) ([]models.Entry, error) {
	return m.fetchLatestSGVsFn(ctx, maxTime, maxEntries)
}
func (m mockEntryRepository) CreateEntries(ctx context.Context, entries []models.Entry) []models.Entry {
	return m.createEntriesFn(ctx, entries)
}

// Helper function to create a test entry
func createTestEntry(oid string) *models.Entry {
	return &models.Entry{
		Oid:       oid,
		Type:      "sgv",
		SgvMgdl:   120,
		Direction: "Flat",
		Device:    "test device",
		Time:      time.Date(2024, 1, 2, 12, 13, 14, 15, time.UTC),
	}
}

// Helper function to set up router with URL parameters
func setupTestRouter(handler http.HandlerFunc, method, path string) *chi.Mux {
	r := chi.NewRouter()
	r.Use(middleware.URLFormat)
	r.Method(method, path, handler)
	return r
}

func TestApiV1_ImportNightscoutEntries(t *testing.T) {
	tests := []struct {
		name           string
		requestBody    string
		expectedStatus int
		expectedBody   string
		mockFn         func(ctx context.Context, nsCfg repository.NightscoutConfig) ([]models.Entry, error)
	}{
		{
			name:           "invalid request body",
			requestBody:    `{invalid json`,
			expectedStatus: http.StatusBadRequest,
			expectedBody:   "invalid request body\n",
		},
		{
			name:           "missing url",
			requestBody:    `{"token": "sometoken-1234567890abcdef"}`,
			expectedStatus: http.StatusBadRequest,
			expectedBody:   "missing url\n",
		},
		{
			name:           "invalid url scheme",
			requestBody:    `{"url": "ftp://example.com", "token": "sometoken-1234567890abcdef"}`,
			expectedStatus: http.StatusBadRequest,
			expectedBody:   "url must be http/https\n",
		},
		{
			name:           "invalid url",
			requestBody:    `{"url": ":", "token": "sometoken-1234567890abcdef"}`,
			expectedStatus: http.StatusBadRequest,
			expectedBody:   "bad url\n",
		},
		{
			name:           "invalid url",
			requestBody:    `{"url": "https://", "token": "sometoken-1234567890abcdef"}`,
			expectedStatus: http.StatusBadRequest,
			expectedBody:   "url must include a hostname\n",
		},
		{
			name:           "missing credentials",
			requestBody:    `{"url": "https://example.com"}`,
			expectedStatus: http.StatusBadRequest,
			expectedBody:   "token or api_secret must be supplied\n",
		},
		{
			name:           "api_secret too short",
			requestBody:    `{"url": "https://example.com", "api_secret": "short"}`,
			expectedStatus: http.StatusBadRequest,
			expectedBody:   "api_secret must be at least 12 characters long\n",
		},
		{
			name:           "token too short",
			requestBody:    `{"url": "https://example.com", "token": "short"}`,
			expectedStatus: http.StatusBadRequest,
			expectedBody:   "token must be at least 17 characters long\n",
		},
		{
			name:           "successful import",
			requestBody:    `{"url": "https://example.com", "token": "sometoken-1234567890abcdef"}`,
			expectedStatus: http.StatusOK,
			mockFn: func(ctx context.Context, nsCfg repository.NightscoutConfig) ([]models.Entry, error) {
				return []models.Entry{
					{
						Time:      time.Date(2024, 3, 1, 12, 1, 0, 0, time.UTC),
						SgvMgdl:   121,
						Type:      "sgv",
						Device:    "test",
						Direction: "FortyFiveUp",
					},
					{
						Time:      time.Date(2024, 3, 1, 12, 0, 0, 0, time.UTC),
						SgvMgdl:   120,
						Type:      "sgv",
						Device:    "test",
						Direction: "Flat",
					},
				}, nil
			},
		},
		{
			name:           "nightscout fetch error",
			requestBody:    `{"url": "https://example.com", "token": "sometoken-1234567890abcdef"}`,
			expectedStatus: http.StatusBadRequest,
			expectedBody:   "Cannot fetch entries from remote nightscout instance\n",
			mockFn: func(ctx context.Context, nsCfg repository.NightscoutConfig) ([]models.Entry, error) {
				return nil, errors.New("fetch failed")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create mock repositories
			mockEntryRepo := &mockEntryRepository{
				createEntriesFn: func(ctx context.Context, entries []models.Entry) []models.Entry {
					return entries
				},
			}
			mockNSRepo := &mockNightscoutRepository{}
			if tt.mockFn != nil {
				mockNSRepo.fetchAllEntriesFn = tt.mockFn
			}

			api := ApiV1{
				EntryRepository:      mockEntryRepo,
				NightscoutRepository: mockNSRepo,
			}

			// Create request
			req := httptest.NewRequest(http.MethodPost, "/api/v1/import", strings.NewReader(tt.requestBody))
			req = req.WithContext(contextWithSilentLogger())
			w := httptest.NewRecorder()

			// Execute request
			api.ImportNightscoutEntries(w, req)

			// Check response
			if w.Code != tt.expectedStatus {
				t.Errorf("expected status %d, got %d", tt.expectedStatus, w.Code)
			}

			if tt.expectedBody != "" && w.Body.String() != tt.expectedBody {
				t.Errorf("expected body %q, got %q", tt.expectedBody, w.Body.String())
			}
		})
	}
}

func TestApiV1_EntryByOid(t *testing.T) {
	tests := []struct {
		name           string
		oid            string
		mockFn         func(ctx context.Context, oid string) (*models.Entry, error)
		expectedStatus int
		expectJSON     bool
	}{
		{
			name: "success",
			oid:  "123",
			mockFn: func(ctx context.Context, oid string) (*models.Entry, error) {
				return createTestEntry(oid), nil
			},
			expectedStatus: http.StatusOK,
			expectJSON:     true,
		},
		{
			name: "not found",
			oid:  "456",
			mockFn: func(ctx context.Context, oid string) (*models.Entry, error) {
				return nil, models.ErrNotFound
			},
			expectedStatus: http.StatusNotFound,
			expectJSON:     false,
		},
		{
			name: "internal error",
			oid:  "789",
			mockFn: func(ctx context.Context, oid string) (*models.Entry, error) {
				return nil, errors.New("internal error")
			},
			expectedStatus: http.StatusInternalServerError,
			expectJSON:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := mockEntryRepository{
				fetchByOidFn: tt.mockFn,
			}
			api := ApiV1{EntryRepository: mock}

			r := setupTestRouter(api.EntryByOid, "GET", "/entry/{oid}")
			req := httptest.NewRequest("GET", "/entry/"+tt.oid, nil)
			w := httptest.NewRecorder()

			r.ServeHTTP(w, req)

			if w.Code != tt.expectedStatus {
				t.Errorf("Expected status %d, got %d", tt.expectedStatus, w.Code)
			}

			if tt.expectJSON {
				var response APIV1EntryResponse
				err := json.NewDecoder(w.Body).Decode(&response)
				if err != nil {
					t.Errorf("Failed to decode response: %v", err)
				}
				if response.Oid != tt.oid {
					t.Errorf("Expected oid %s, got %s", tt.oid, response.Oid)
				}
			}
		})
	}
}

func TestApiV1_LatestEntry(t *testing.T) {
	tests := []struct {
		name           string
		mockFn         func(ctx context.Context, maxTime time.Time) (*models.Entry, error)
		extension      string
		expectedStatus int
		expectJSON     bool
		expectTSV      bool
	}{
		{
			name: "success",
			mockFn: func(ctx context.Context, maxTime time.Time) (*models.Entry, error) {
				return createTestEntry("123"), nil
			},
			extension:      ".json",
			expectedStatus: http.StatusOK,
			expectJSON:     true,
			expectTSV:      false,
		},
		{
			name: "success tsv",
			mockFn: func(ctx context.Context, maxTime time.Time) (*models.Entry, error) {
				return createTestEntry("123"), nil
			},
			extension:      "",
			expectedStatus: http.StatusOK,
			expectJSON:     false,
			expectTSV:      true,
		},
		{
			name: "not found",
			mockFn: func(ctx context.Context, maxTime time.Time) (*models.Entry, error) {
				return nil, models.ErrNotFound
			},
			extension:      ".json",
			expectedStatus: http.StatusNotFound,
			expectJSON:     false,
			expectTSV:      false,
		},
		{
			name: "internal error",
			mockFn: func(ctx context.Context, maxTime time.Time) (*models.Entry, error) {
				return nil, errors.New("internal error")
			},
			extension:      ".json",
			expectedStatus: http.StatusInternalServerError,
			expectJSON:     false,
			expectTSV:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := mockEntryRepository{
				fetchLatestFn: tt.mockFn,
			}
			api := ApiV1{EntryRepository: mock}

			r := setupTestRouter(api.LatestEntry, "GET", "/entries/current")
			req := httptest.NewRequest("GET", "/entries/current"+tt.extension, nil)
			w := httptest.NewRecorder()

			r.ServeHTTP(w, req)

			if w.Code != tt.expectedStatus {
				t.Errorf("Expected status %d, got %d", tt.expectedStatus, w.Code)
			}

			if tt.expectJSON {
				var response []APIV1EntryResponse
				err := json.NewDecoder(w.Body).Decode(&response)
				if err != nil {
					t.Errorf("Failed to decode response: %v", err)
				}
				assert.Len(t, response, 1)
			}
			if tt.expectTSV {
				body, _ := io.ReadAll(w.Body)
				lines := bytes.Split(body, []byte("\n"))
				assert.Len(t, lines, 1)

				line := lines[0]
				fields := bytes.Split(line, []byte("\t"))
				assert.Len(t, fields, 5)

				// timestamps rounded to nearest second
				assert.Equal(t, `"2024-01-02T12:13:14.000Z"`, string(fields[0]))
				assert.Equal(t, "1704197594000", string(fields[1]))
				assert.Equal(t, "120", string(fields[2]))
				assert.Equal(t, `"Flat"`, string(fields[3]))
				assert.Equal(t, `"test device"`, string(fields[4]))
			}
		})
	}
}

func TestApiV1_ListEntries(t *testing.T) {
	tests := []struct {
		name           string
		queryCount     string
		mockFn         func(ctx context.Context, maxTime time.Time, maxEntries int) ([]models.Entry, error)
		expectedStatus int
		expectJSON     bool
		expectedLen    int
	}{
		{
			name:       "success with default count",
			queryCount: "",
			mockFn: func(ctx context.Context, maxTime time.Time, maxEntries int) ([]models.Entry, error) {
				entries := make([]models.Entry, maxEntries)
				for i := 0; i < maxEntries; i++ {
					entries[i] = *createTestEntry("test" + string(rune(i)))
				}
				return entries, nil
			},
			expectedStatus: http.StatusOK,
			expectJSON:     true,
			expectedLen:    20, // default count
		},
		{
			name:       "success with custom count",
			queryCount: "5",
			mockFn: func(ctx context.Context, maxTime time.Time, maxEntries int) ([]models.Entry, error) {
				entries := make([]models.Entry, maxEntries)
				for i := 0; i < maxEntries; i++ {
					entries[i] = *createTestEntry("test" + string(rune(i)))
				}
				return entries, nil
			},
			expectedStatus: http.StatusOK,
			expectJSON:     true,
			expectedLen:    5,
		},
		{
			name:       "invalid count parameter",
			queryCount: "invalid",
			mockFn: func(ctx context.Context, maxTime time.Time, maxEntries int) ([]models.Entry, error) {
				return nil, nil
			},
			expectedStatus: http.StatusBadRequest,
			expectJSON:     false,
		},
		{
			name:       "count too small",
			queryCount: "0",
			mockFn: func(ctx context.Context, maxTime time.Time, maxEntries int) ([]models.Entry, error) {
				return nil, nil
			},
			expectedStatus: http.StatusBadRequest,
			expectJSON:     false,
		},
		{
			name:       "count too large",
			queryCount: "50001",
			mockFn: func(ctx context.Context, maxTime time.Time, maxEntries int) ([]models.Entry, error) {
				return nil, nil
			},
			expectedStatus: http.StatusBadRequest,
			expectJSON:     false,
		},
		{
			name:       "repository error",
			queryCount: "10",
			mockFn: func(ctx context.Context, maxTime time.Time, maxEntries int) ([]models.Entry, error) {
				return nil, errors.New("internal error")
			},
			expectedStatus: http.StatusInternalServerError,
			expectJSON:     false,
		},
		{
			name:       "empty result",
			queryCount: "10",
			mockFn: func(ctx context.Context, maxTime time.Time, maxEntries int) ([]models.Entry, error) {
				return []models.Entry{}, nil
			},
			expectedStatus: http.StatusOK,
			expectJSON:     true,
			expectedLen:    0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := mockEntryRepository{
				fetchLatestListFn: tt.mockFn,
			}
			api := ApiV1{EntryRepository: mock}

			r := setupTestRouter(api.ListEntries, "GET", "/entries")
			url := "/entries.json"
			if tt.queryCount != "" {
				url += "?count=" + tt.queryCount
			}
			req := httptest.NewRequest("GET", url, nil)
			w := httptest.NewRecorder()

			r.ServeHTTP(w, req)

			if w.Code != tt.expectedStatus {
				t.Errorf("Expected status %d, got %d", tt.expectedStatus, w.Code)
			}

			if tt.expectJSON {
				if w.Body.Len() == 0 {
					t.Error("Expected non-empty response body")
					return
				}
				var response []APIV1EntryResponse
				err := json.NewDecoder(w.Body).Decode(&response)
				if err != nil {
					t.Errorf("Failed to decode response: %v", err)
				}
				if len(response) != tt.expectedLen {
					t.Errorf("Expected %d entries, got %d", tt.expectedLen, len(response))
				}
			}
		})
	}
}

func TestApiV1_renderTreatmentList(t *testing.T) {
	tests := []struct {
		name           string
		treatments     []models.Treatment
		expectedStatus int
		expectedBody   string
	}{
		{
			name:           "empty treatment list",
			treatments:     []models.Treatment{},
			expectedStatus: http.StatusOK,
			expectedBody:   "[]",
		},
		{
			name: "single treatment",
			treatments: []models.Treatment{
				{
					ID:   "treatment-123",
					Time: time.Date(2024, 3, 1, 12, 0, 1, 2, time.UTC),
					Fields: map[string]interface{}{
						"eventType": "Meal Bolus",
						"insulin":   5.5,
						"carbs":     45,
						"notes":     "Lunch",
					},
				},
			},
			expectedStatus: http.StatusOK,
			expectedBody: `[{"_id":"treatment-123","eventType":"Meal Bolus",
				"created_at":"2024-03-01T12:00:01.000Z", "mills":1709294401000,
				"insulin":5.5,"carbs":45,"notes":"Lunch"}]`,
		},
		{
			name: "multiple treatments with different field combinations",
			treatments: []models.Treatment{
				{
					ID:   "treatment-123",
					Time: time.Date(2024, 3, 1, 12, 1, 2, 654321, time.UTC),
					Fields: map[string]interface{}{
						"eventType": "Meal Bolus",
						"insulin":   5.5,
						"carbs":     45,
						"notes":     "Lunch",
					},
				},
				{
					ID:   "treatment-124",
					Time: time.Date(2024, 3, 1, 15, 30, 3, 987654321, time.UTC),
					Fields: map[string]interface{}{
						"eventType": "Correction Bolus",
						"insulin":   1.5,
						"notes":     "High glucose correction",
					},
				},
			},
			expectedStatus: http.StatusOK,
			expectedBody: `[{"_id":"treatment-123","eventType":"Meal Bolus",
				"created_at":"2024-03-01T12:01:02.000Z","mills":1709294462000,
				"insulin":5.5,"carbs":45,"notes":"Lunch"},
				{"_id":"treatment-124","eventType":"Correction Bolus",
				"created_at":"2024-03-01T15:30:03.987Z", "mills":1709307003987,
				"insulin":1.5,"notes":"High glucose correction"}]`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api := &ApiV1{}

			// Create request and response recorder
			req := httptest.NewRequest(http.MethodGet, "/api/v1/treatments", nil)
			req = req.WithContext(contextWithSilentLogger())
			w := httptest.NewRecorder()

			// Execute request
			api.renderTreatmentList(w, req, tt.treatments)

			// Check response
			assert.Equal(t, tt.expectedStatus, w.Code)
			assert.JSONEq(t, tt.expectedBody, w.Body.String())
		})
	}
}
