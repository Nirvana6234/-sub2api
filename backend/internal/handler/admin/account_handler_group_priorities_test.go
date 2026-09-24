package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// 该接口是 TransitHub 连接健康探活的优先级回写入口。契约（updates 包裹 +
// account_id/group_id/priority 字段名）由 TransitHub 的
// sub2APIAccountGroupPriorityRequest 决定：字段一旦对不上，它拿到的响应无法解析，
// 会以 admin.upstream.errors.invalidResponse 无限重试，而优先级永远停在旧值。
// 2026-09-16 生产上就是因为本接口缺失（404）导致降级账号的优先级一直没生效。
type groupPrioritiesAdminService struct {
	*stubAdminService

	received []service.AccountGroupPriorityUpdate
	updated  int
	err      error
}

func (s *groupPrioritiesAdminService) UpdateAccountGroupPriorities(
	_ context.Context, updates []service.AccountGroupPriorityUpdate,
) (int, error) {
	s.received = append(s.received, updates...)
	if s.err != nil {
		return 0, s.err
	}
	if s.updated > 0 {
		return s.updated, nil
	}
	return len(updates), nil
}

func setupGroupPrioritiesRouter(adminSvc *groupPrioritiesAdminService) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	handler := NewAccountHandler(adminSvc, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	router.POST("/api/v1/admin/accounts/group-priorities", handler.UpdateGroupPriorities)
	return router
}

func postGroupPriorities(router *gin.Engine, body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/admin/accounts/group-priorities",
		bytes.NewBufferString(body),
	)
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(rec, req)
	return rec
}

func TestUpdateGroupPrioritiesAcceptsTransitHubContract(t *testing.T) {
	adminSvc := &groupPrioritiesAdminService{stubAdminService: newStubAdminService()}
	router := setupGroupPrioritiesRouter(adminSvc)

	// 这段 JSON 与 TransitHub 实际发出的报文同形。
	rec := postGroupPriorities(router,
		`{"updates":[{"account_id":97,"group_id":34,"priority":500},{"account_id":221,"group_id":34,"priority":10000}]}`)

	require.Equal(t, http.StatusOK, rec.Code)

	var payload struct {
		Data struct {
			Updated   int `json:"updated"`
			Requested int `json:"requested"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &payload))
	require.Equal(t, 2, payload.Data.Requested)
	require.Equal(t, 2, payload.Data.Updated)

	require.Equal(t, []service.AccountGroupPriorityUpdate{
		{AccountID: 97, GroupID: 34, Priority: 500},
		{AccountID: 221, GroupID: 34, Priority: 10000},
	}, adminSvc.received)
}

// 命中行数少于请求数是正常情况（账号可能刚被移出分组），必须如实返回而不是报错，
// 否则 TransitHub 会把一次正常的竞争当成失败反复重试。
func TestUpdateGroupPrioritiesReportsPartialHit(t *testing.T) {
	adminSvc := &groupPrioritiesAdminService{stubAdminService: newStubAdminService(), updated: 1}
	router := setupGroupPrioritiesRouter(adminSvc)

	rec := postGroupPriorities(router,
		`{"updates":[{"account_id":97,"group_id":34,"priority":500},{"account_id":999,"group_id":34,"priority":600}]}`)

	require.Equal(t, http.StatusOK, rec.Code)

	var payload struct {
		Data struct {
			Updated   int `json:"updated"`
			Requested int `json:"requested"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &payload))
	require.Equal(t, 1, payload.Data.Updated)
	require.Equal(t, 2, payload.Data.Requested)
}

func TestUpdateGroupPrioritiesRejectsInvalidInput(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "空 updates", body: `{"updates":[]}`},
		{name: "缺少 updates", body: `{}`},
		{name: "account_id 为 0", body: `{"updates":[{"account_id":0,"group_id":34,"priority":1}]}`},
		{name: "group_id 为负", body: `{"updates":[{"account_id":97,"group_id":-1,"priority":1}]}`},
		{name: "priority 为负", body: `{"updates":[{"account_id":97,"group_id":34,"priority":-5}]}`},
		{name: "非法 JSON", body: `{"updates":`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			adminSvc := &groupPrioritiesAdminService{stubAdminService: newStubAdminService()}
			router := setupGroupPrioritiesRouter(adminSvc)

			rec := postGroupPriorities(router, tt.body)

			require.Equal(t, http.StatusBadRequest, rec.Code)
			require.Empty(t, adminSvc.received, "校验失败时不得落库")
		})
	}
}

// 批量上限防止单次请求打爆数据库。
func TestUpdateGroupPrioritiesRejectsOversizedBatch(t *testing.T) {
	adminSvc := &groupPrioritiesAdminService{stubAdminService: newStubAdminService()}
	router := setupGroupPrioritiesRouter(adminSvc)

	var sb strings.Builder
	sb.WriteString(`{"updates":[`)
	for i := 0; i < 501; i++ {
		if i > 0 {
			sb.WriteString(",")
		}
		sb.WriteString(`{"account_id":1,"group_id":1,"priority":1}`)
	}
	sb.WriteString(`]}`)

	rec := postGroupPriorities(router, sb.String())

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Empty(t, adminSvc.received)
}

func TestUpdateGroupPrioritiesPropagatesServiceError(t *testing.T) {
	adminSvc := &groupPrioritiesAdminService{
		stubAdminService: newStubAdminService(),
		err:              errors.New("db down"),
	}
	router := setupGroupPrioritiesRouter(adminSvc)

	rec := postGroupPriorities(router,
		`{"updates":[{"account_id":97,"group_id":34,"priority":500}]}`)

	require.GreaterOrEqual(t, rec.Code, http.StatusInternalServerError)
}
