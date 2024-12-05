package controllers

import (
	"context"
	"encoding/json"
	"errors"
	repository "github.com/adamlounds/nightscout-go/adapters"
	"github.com/adamlounds/nightscout-go/models"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
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
func (m mockEntryRepository) CreateEntries(ctx context.Context, entries []models.Entry) []models.Entry {
	return m.createEntriesFn(ctx, entries)
}

// Helper function to create a test entry
func createTestEntry(oid string) *models.Entry {
	return &models.Entry{
		Oid:         oid,
		Type:        "sgv",
		SgvMgdl:     120,
		Direction:   "Flat",
		CreatedTime: time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC),
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
		expectedStatus int
		expectJSON     bool
	}{
		{
			name: "success",
			mockFn: func(ctx context.Context, maxTime time.Time) (*models.Entry, error) {
				return createTestEntry("123"), nil
			},
			expectedStatus: http.StatusOK,
			expectJSON:     true,
		},
		{
			name: "not found",
			mockFn: func(ctx context.Context, maxTime time.Time) (*models.Entry, error) {
				return nil, models.ErrNotFound
			},
			expectedStatus: http.StatusNotFound,
			expectJSON:     false,
		},
		{
			name: "internal error",
			mockFn: func(ctx context.Context, maxTime time.Time) (*models.Entry, error) {
				return nil, errors.New("internal error")
			},
			expectedStatus: http.StatusInternalServerError,
			expectJSON:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := mockEntryRepository{
				fetchLatestFn: tt.mockFn,
			}
			api := ApiV1{EntryRepository: mock}

			req := httptest.NewRequest("GET", "/entries/current", nil)
			w := httptest.NewRecorder()
			api.LatestEntry(w, req)

			if w.Code != tt.expectedStatus {
				t.Errorf("Expected status %d, got %d", tt.expectedStatus, w.Code)
			}

			if tt.expectJSON {
				var response APIV1EntryResponse
				err := json.NewDecoder(w.Body).Decode(&response)
				if err != nil {
					t.Errorf("Failed to decode response: %v", err)
				}
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
