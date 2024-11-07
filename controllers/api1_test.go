package controllers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/adamlounds/nightscout-go/models"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

// mockEventRepository implements EventRepository interface for testing
type mockEventRepository struct {
	fetchByOidFn      func(ctx context.Context, oid string) (*models.Event, error)
	fetchLatestFn     func(ctx context.Context) (*models.Event, error)
	fetchLatestListFn func(ctx context.Context, maxEvents int) ([]models.Event, error)
}

func (m mockEventRepository) FetchEventByOid(ctx context.Context, oid string) (*models.Event, error) {
	return m.fetchByOidFn(ctx, oid)
}

func (m mockEventRepository) FetchLatestEvent(ctx context.Context) (*models.Event, error) {
	return m.fetchLatestFn(ctx)
}

func (m mockEventRepository) FetchLatestEvents(ctx context.Context, maxEvents int) ([]models.Event, error) {
	return m.fetchLatestListFn(ctx, maxEvents)
}

// Helper function to create a test event
func createTestEvent(oid string) *models.Event {
	return &models.Event{
		Oid:         oid,
		Type:        "sgv",
		Mgdl:        120,
		Direction:   "Flat",
		CreatedTime: time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC),
	}
}

// Helper function to setup router with URL parameters
func setupTestRouter(handler http.HandlerFunc, method, path string) *chi.Mux {
	r := chi.NewRouter()
	r.Use(middleware.URLFormat)
	r.Method(method, path, handler)
	return r
}

func TestApiV1_EntryByOid(t *testing.T) {
	tests := []struct {
		name           string
		oid            string
		mockFn         func(ctx context.Context, oid string) (*models.Event, error)
		expectedStatus int
		expectJSON     bool
	}{
		{
			name: "success",
			oid:  "123",
			mockFn: func(ctx context.Context, oid string) (*models.Event, error) {
				return createTestEvent(oid), nil
			},
			expectedStatus: http.StatusOK,
			expectJSON:     true,
		},
		{
			name: "not found",
			oid:  "456",
			mockFn: func(ctx context.Context, oid string) (*models.Event, error) {
				return nil, models.ErrNotFound
			},
			expectedStatus: http.StatusNotFound,
			expectJSON:     false,
		},
		{
			name: "internal error",
			oid:  "789",
			mockFn: func(ctx context.Context, oid string) (*models.Event, error) {
				return nil, errors.New("internal error")
			},
			expectedStatus: http.StatusInternalServerError,
			expectJSON:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := mockEventRepository{
				fetchByOidFn: tt.mockFn,
			}
			api := ApiV1{EventRepository: mock}

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
		mockFn         func(ctx context.Context) (*models.Event, error)
		expectedStatus int
		expectJSON     bool
	}{
		{
			name: "success",
			mockFn: func(ctx context.Context) (*models.Event, error) {
				return createTestEvent("123"), nil
			},
			expectedStatus: http.StatusOK,
			expectJSON:     true,
		},
		{
			name: "not found",
			mockFn: func(ctx context.Context) (*models.Event, error) {
				return nil, models.ErrNotFound
			},
			expectedStatus: http.StatusNotFound,
			expectJSON:     false,
		},
		{
			name: "internal error",
			mockFn: func(ctx context.Context) (*models.Event, error) {
				return nil, errors.New("internal error")
			},
			expectedStatus: http.StatusInternalServerError,
			expectJSON:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := mockEventRepository{
				fetchLatestFn: tt.mockFn,
			}
			api := ApiV1{EventRepository: mock}

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
		mockFn         func(ctx context.Context, maxEvents int) ([]models.Event, error)
		expectedStatus int
		expectJSON     bool
		expectedLen    int
	}{
		{
			name:       "success with default count",
			queryCount: "",
			mockFn: func(ctx context.Context, maxEvents int) ([]models.Event, error) {
				events := make([]models.Event, maxEvents)
				for i := 0; i < maxEvents; i++ {
					events[i] = *createTestEvent("test" + string(rune(i)))
				}
				return events, nil
			},
			expectedStatus: http.StatusOK,
			expectJSON:     true,
			expectedLen:    20, // default count
		},
		{
			name:       "success with custom count",
			queryCount: "5",
			mockFn: func(ctx context.Context, maxEvents int) ([]models.Event, error) {
				events := make([]models.Event, maxEvents)
				for i := 0; i < maxEvents; i++ {
					events[i] = *createTestEvent("test" + string(rune(i)))
				}
				return events, nil
			},
			expectedStatus: http.StatusOK,
			expectJSON:     true,
			expectedLen:    5,
		},
		{
			name:       "invalid count parameter",
			queryCount: "invalid",
			mockFn: func(ctx context.Context, maxEvents int) ([]models.Event, error) {
				return nil, nil
			},
			expectedStatus: http.StatusBadRequest,
			expectJSON:     false,
		},
		{
			name:       "count too small",
			queryCount: "0",
			mockFn: func(ctx context.Context, maxEvents int) ([]models.Event, error) {
				return nil, nil
			},
			expectedStatus: http.StatusBadRequest,
			expectJSON:     false,
		},
		{
			name:       "count too large",
			queryCount: "50001",
			mockFn: func(ctx context.Context, maxEvents int) ([]models.Event, error) {
				return nil, nil
			},
			expectedStatus: http.StatusBadRequest,
			expectJSON:     false,
		},
		{
			name:       "repository error",
			queryCount: "10",
			mockFn: func(ctx context.Context, maxEvents int) ([]models.Event, error) {
				return nil, errors.New("internal error")
			},
			expectedStatus: http.StatusInternalServerError,
			expectJSON:     false,
		},
		{
			name:       "empty result",
			queryCount: "10",
			mockFn: func(ctx context.Context, maxEvents int) ([]models.Event, error) {
				return []models.Event{}, nil
			},
			expectedStatus: http.StatusOK,
			expectJSON:     true,
			expectedLen:    0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := mockEventRepository{
				fetchLatestListFn: tt.mockFn,
			}
			api := ApiV1{EventRepository: mock}

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
