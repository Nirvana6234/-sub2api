package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type countingUsageListRepo struct {
	service.UsageLogRepository
	mu    sync.Mutex
	calls int
}

func (r *countingUsageListRepo) ListWithFilters(_ context.Context, params pagination.PaginationParams, _ usagestats.UsageLogFilters) ([]service.UsageLog, *pagination.PaginationResult, error) {
	r.mu.Lock()
	r.calls++
	r.mu.Unlock()
	return []service.UsageLog{{ID: int64(params.Page), Model: "gpt-5.5"}}, &pagination.PaginationResult{Total: 42, Page: params.Page, PageSize: params.PageSize}, nil
}

type memoryUsageResultStore struct {
	mu     sync.Mutex
	values map[string][]byte
}

func (s *memoryUsageResultStore) Get(_ context.Context, key string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if value, ok := s.values[key]; ok {
		return value, nil
	}
	return nil, service.ErrResultCacheMiss
}

func (s *memoryUsageResultStore) Set(_ context.Context, key string, value []byte, _ time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.values[key] = value
	return nil
}

func TestAdminUsageListServesRepeatedQueriesFromCache(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo := &countingUsageListRepo{}
	cache := service.NewResultCache(&memoryUsageResultStore{values: map[string][]byte{}})
	handler := NewUsageHandler(service.NewUsageService(repo, nil, nil, nil), nil, nil, nil, cache)
	router := gin.New()
	router.GET("/admin/usage", handler.List)

	get := func(target string) string {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))
		require.Equal(t, http.StatusOK, rec.Code)
		return rec.Body.String()
	}

	first := get("/admin/usage?group_id=3&page=1&page_size=100&sort_by=created_at&sort_order=desc")
	second := get("/admin/usage?group_id=3&page=1&page_size=100&sort_by=created_at&sort_order=desc")
	require.Equal(t, first, second, "a cached page must render identically")
	require.Equal(t, 1, repo.calls)

	// Parameter order does not change the query, so it shares the entry.
	get("/admin/usage?sort_order=desc&sort_by=created_at&page_size=100&page=1&group_id=3")
	require.Equal(t, 1, repo.calls)

	// A different page is a different query.
	get("/admin/usage?group_id=3&page=2&page_size=100&sort_by=created_at&sort_order=desc")
	require.Equal(t, 2, repo.calls)
}
