package admin

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/handler/dto"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/pkg/timezone"
	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

// UsageHandler handles admin usage-related requests
type UsageHandler struct {
	usageService   *service.UsageService
	apiKeyService  *service.APIKeyService
	adminService   service.AdminService
	cleanupService *service.UsageCleanupService
}

// NewUsageHandler creates a new admin usage handler
func NewUsageHandler(
	usageService *service.UsageService,
	apiKeyService *service.APIKeyService,
	adminService service.AdminService,
	cleanupService *service.UsageCleanupService,
) *UsageHandler {
	return &UsageHandler{
		usageService:   usageService,
		apiKeyService:  apiKeyService,
		adminService:   adminService,
		cleanupService: cleanupService,
	}
}

// CreateUsageCleanupTaskRequest represents cleanup task creation request
type CreateUsageCleanupTaskRequest struct {
	StartDate   string  `json:"start_date"`
	EndDate     string  `json:"end_date"`
	UserID      *int64  `json:"user_id"`
	APIKeyID    *int64  `json:"api_key_id"`
	AccountID   *int64  `json:"account_id"`
	GroupID     *int64  `json:"group_id"`
	Model       *string `json:"model"`
	RequestType *string `json:"request_type"`
	Stream      *bool   `json:"stream"`
	BillingType *int8   `json:"billing_type"`
	Timezone    string  `json:"timezone"`
}

// List handles listing all usage records with filters
// GET /api/v1/admin/usage
func (h *UsageHandler) List(c *gin.Context) {
	page, pageSize := response.ParsePagination(c)
	exactTotal := false
	if exactTotalRaw := strings.TrimSpace(c.Query("exact_total")); exactTotalRaw != "" {
		parsed, err := strconv.ParseBool(exactTotalRaw)
		if err != nil {
			response.BadRequest(c, "Invalid exact_total value, use true or false")
			return
		}
		exactTotal = parsed
	}

	// Parse filters
	var userID, apiKeyID, accountID, groupID int64
	excludedUserIDs, err := parseExcludedUserIDsFromQuery(c)
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	if userIDStr := c.Query("user_id"); userIDStr != "" {
		id, err := strconv.ParseInt(userIDStr, 10, 64)
		if err != nil {
			response.BadRequest(c, "Invalid user_id")
			return
		}
		userID = id
	}

	if apiKeyIDStr := c.Query("api_key_id"); apiKeyIDStr != "" {
		id, err := strconv.ParseInt(apiKeyIDStr, 10, 64)
		if err != nil {
			response.BadRequest(c, "Invalid api_key_id")
			return
		}
		apiKeyID = id
	}

	if accountIDStr := c.Query("account_id"); accountIDStr != "" {
		id, err := strconv.ParseInt(accountIDStr, 10, 64)
		if err != nil {
			response.BadRequest(c, "Invalid account_id")
			return
		}
		accountID = id
	}

	if groupIDStr := c.Query("group_id"); groupIDStr != "" {
		id, err := strconv.ParseInt(groupIDStr, 10, 64)
		if err != nil {
			response.BadRequest(c, "Invalid group_id")
			return
		}
		groupID = id
	}

	model := c.Query("model")
	requestID := strings.TrimSpace(c.Query("request_id"))
	billingMode := strings.TrimSpace(c.Query("billing_mode"))

	var requestType *int16
	var stream *bool
	if requestTypeStr := strings.TrimSpace(c.Query("request_type")); requestTypeStr != "" {
		parsed, err := service.ParseUsageRequestType(requestTypeStr)
		if err != nil {
			response.BadRequest(c, err.Error())
			return
		}
		value := int16(parsed)
		requestType = &value
	} else if streamStr := c.Query("stream"); streamStr != "" {
		val, err := strconv.ParseBool(streamStr)
		if err != nil {
			response.BadRequest(c, "Invalid stream value, use true or false")
			return
		}
		stream = &val
	}

	nativeCompactionV2, err := parseOptionalBoolDashboardFilter(c, "native_compaction_v2")
	if err != nil {
		response.BadRequest(c, "Invalid native_compaction_v2 value, use true or false")
		return
	}

	var billingType *int8
	if billingTypeStr := c.Query("billing_type"); billingTypeStr != "" {
		val, err := strconv.ParseInt(billingTypeStr, 10, 8)
		if err != nil {
			response.BadRequest(c, "Invalid billing_type")
			return
		}
		bt := int8(val)
		billingType = &bt
	}

	var upstreamModelMismatch *bool
	if raw := strings.TrimSpace(c.Query("upstream_model_mismatch")); raw != "" {
		value, err := strconv.ParseBool(raw)
		if err != nil {
			response.BadRequest(c, "Invalid upstream_model_mismatch value, use true or false")
			return
		}
		upstreamModelMismatch = &value
	}

	// Parse date range
	var startTime, endTime *time.Time
	userTZ := c.Query("timezone") // Get user's timezone from request
	if startDateStr := c.Query("start_date"); startDateStr != "" {
		t, err := timezone.ParseInUserLocation("2006-01-02", startDateStr, userTZ)
		if err != nil {
			response.BadRequest(c, "Invalid start_date format, use YYYY-MM-DD")
			return
		}
		startTime = &t
	}

	if endDateStr := c.Query("end_date"); endDateStr != "" {
		t, err := timezone.ParseInUserLocation("2006-01-02", endDateStr, userTZ)
		if err != nil {
			response.BadRequest(c, "Invalid end_date format, use YYYY-MM-DD")
			return
		}
		// Use half-open range [start, end), move to next calendar day start (DST-safe).
		t = t.AddDate(0, 0, 1)
		endTime = &t
	}

	params := pagination.PaginationParams{
		Page:      page,
		PageSize:  pageSize,
		SortBy:    c.DefaultQuery("sort_by", "created_at"),
		SortOrder: c.DefaultQuery("sort_order", "desc"),
	}
	filters := usagestats.UsageLogFilters{
		UserID:                userID,
		ExcludedUserIDs:       excludedUserIDs,
		APIKeyID:              apiKeyID,
		AccountID:             accountID,
		GroupID:               groupID,
		RequestID:             requestID,
		Model:                 model,
		ModelFilterSource:     usagestats.ModelSourceRequested,
		RequestType:           requestType,
		Stream:                stream,
		NativeCompactionV2:    nativeCompactionV2,
		BillingType:           billingType,
		BillingMode:           billingMode,
		UpstreamModelMismatch: upstreamModelMismatch,
		StartTime:             startTime,
		EndTime:               endTime,
		ExactTotal:            exactTotal,
	}

	records, result, err := h.usageService.ListWithFilters(c.Request.Context(), params, filters)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	out := make([]dto.AdminUsageLog, 0, len(records))
	for i := range records {
		out = append(out, *dto.UsageLogFromServiceAdmin(&records[i]))
	}
	response.Paginated(c, out, result.Total, page, pageSize)
}

func parseExcludedUserIDsFromQuery(c *gin.Context) ([]int64, error) {
	values := append([]string{}, c.QueryArray("exclude_user_ids")...)
	// Axios serializes array query parameters as exclude_user_ids[]=1 by default.
	values = append(values, c.QueryArray("exclude_user_ids[]")...)
	return parseExcludedUserIDs(values)
}

func parseExcludedUserIDs(raw []string) ([]int64, error) {
	ids := make([]int64, 0, len(raw))
	seen := make(map[int64]struct{}, len(raw))
	for _, value := range raw {
		for _, part := range strings.Split(value, ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			id, err := strconv.ParseInt(part, 10, 64)
			if err != nil || id <= 0 {
				return nil, fmt.Errorf("Invalid exclude_user_ids value")
			}
			if _, ok := seen[id]; !ok {
				seen[id] = struct{}{}
				ids = append(ids, id)
			}
		}
	}
	return ids, nil
}

// Stats handles getting usage statistics with filters
// GET /api/v1/admin/usage/stats
func (h *UsageHandler) Stats(c *gin.Context) {
	// Parse filters - same as List endpoint
	var userID, apiKeyID, accountID, groupID int64
	if userIDStr := c.Query("user_id"); userIDStr != "" {
		id, err := strconv.ParseInt(userIDStr, 10, 64)
		if err != nil {
			response.BadRequest(c, "Invalid user_id")
			return
		}
		userID = id
	}

	if apiKeyIDStr := c.Query("api_key_id"); apiKeyIDStr != "" {
		id, err := strconv.ParseInt(apiKeyIDStr, 10, 64)
		if err != nil {
			response.BadRequest(c, "Invalid api_key_id")
			return
		}
		apiKeyID = id
	}

	if accountIDStr := c.Query("account_id"); accountIDStr != "" {
		id, err := strconv.ParseInt(accountIDStr, 10, 64)
		if err != nil {
			response.BadRequest(c, "Invalid account_id")
			return
		}
		accountID = id
	}

	if groupIDStr := c.Query("group_id"); groupIDStr != "" {
		id, err := strconv.ParseInt(groupIDStr, 10, 64)
		if err != nil {
			response.BadRequest(c, "Invalid group_id")
			return
		}
		groupID = id
	}

	model := c.Query("model")
	billingMode := strings.TrimSpace(c.Query("billing_mode"))

	var requestType *int16
	var stream *bool
	if requestTypeStr := strings.TrimSpace(c.Query("request_type")); requestTypeStr != "" {
		parsed, err := service.ParseUsageRequestType(requestTypeStr)
		if err != nil {
			response.BadRequest(c, err.Error())
			return
		}
		value := int16(parsed)
		requestType = &value
	} else if streamStr := c.Query("stream"); streamStr != "" {
		val, err := strconv.ParseBool(streamStr)
		if err != nil {
			response.BadRequest(c, "Invalid stream value, use true or false")
			return
		}
		stream = &val
	}

	nativeCompactionV2, err := parseOptionalBoolDashboardFilter(c, "native_compaction_v2")
	if err != nil {
		response.BadRequest(c, "Invalid native_compaction_v2 value, use true or false")
		return
	}

	var billingType *int8
	if billingTypeStr := c.Query("billing_type"); billingTypeStr != "" {
		val, err := strconv.ParseInt(billingTypeStr, 10, 8)
		if err != nil {
			response.BadRequest(c, "Invalid billing_type")
			return
		}
		bt := int8(val)
		billingType = &bt
	}

	var upstreamModelMismatch *bool
	if raw := strings.TrimSpace(c.Query("upstream_model_mismatch")); raw != "" {
		value, err := strconv.ParseBool(raw)
		if err != nil {
			response.BadRequest(c, "Invalid upstream_model_mismatch value, use true or false")
			return
		}
		upstreamModelMismatch = &value
	}

	// Parse date range
	userTZ := c.Query("timezone")
	now := timezone.NowInUserLocation(userTZ)
	var startTime, endTime time.Time

	startDateStr := c.Query("start_date")
	endDateStr := c.Query("end_date")

	if startDateStr != "" && endDateStr != "" {
		var err error
		startTime, err = timezone.ParseInUserLocation("2006-01-02", startDateStr, userTZ)
		if err != nil {
			response.BadRequest(c, "Invalid start_date format, use YYYY-MM-DD")
			return
		}
		endTime, err = timezone.ParseInUserLocation("2006-01-02", endDateStr, userTZ)
		if err != nil {
			response.BadRequest(c, "Invalid end_date format, use YYYY-MM-DD")
			return
		}
		// 与 SQL 条件 created_at < end 对齐，使用次日 00:00 作为上边界（DST-safe）。
		endTime = endTime.AddDate(0, 0, 1)
	} else {
		period := c.DefaultQuery("period", "today")
		switch period {
		case "today":
			startTime = timezone.StartOfDayInUserLocation(now, userTZ)
		case "week":
			startTime = now.AddDate(0, 0, -7)
		case "month":
			startTime = now.AddDate(0, -1, 0)
		default:
			startTime = timezone.StartOfDayInUserLocation(now, userTZ)
		}
		endTime = now
	}

	// Build filters and call GetStatsWithFilters
	filters := usagestats.UsageLogFilters{
		UserID:                userID,
		APIKeyID:              apiKeyID,
		AccountID:             accountID,
		GroupID:               groupID,
		Model:                 model,
		ModelFilterSource:     usagestats.ModelSourceRequested,
		RequestType:           requestType,
		Stream:                stream,
		NativeCompactionV2:    nativeCompactionV2,
		BillingType:           billingType,
		BillingMode:           billingMode,
		UpstreamModelMismatch: upstreamModelMismatch,
		StartTime:             &startTime,
		EndTime:               &endTime,
	}

	var stats *usagestats.UsageStats
	// nocache: 绕过缓存直接回源,刷新者本人拿最新;不回写缓存(管理台"我刷新我自己拿最新"语义,非全局失效)。
	if parseBoolQueryWithDefault(c.Query("nocache"), false) {
		s, err := h.usageService.GetStatsWithFilters(c.Request.Context(), filters)
		if err != nil {
			response.ErrorFrom(c, err)
			return
		}
		stats = s
		c.Header("X-Usage-Stats-Cache", "bypass")
	} else {
		s, hit, err := h.getStatsCached(c.Request.Context(), filters)
		if err != nil {
			response.ErrorFrom(c, err)
			return
		}
		stats = s
		c.Header("X-Usage-Stats-Cache", cacheStatusValue(hit))
	}

	response.Success(c, stats)
}

// SearchUsers handles searching users by email keyword
// GET /api/v1/admin/usage/search-users
func (h *UsageHandler) SearchUsers(c *gin.Context) {
	keyword := c.Query("q")
	if keyword == "" {
		response.Success(c, []any{})
		return
	}

	// Limit to 30 results
	users, _, err := h.adminService.ListUsers(c.Request.Context(), 1, 30, service.UserListFilters{Search: keyword, IncludeDeleted: true}, "email", "asc")
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	// Return simplified user list (only id, email and deleted flag)
	type SimpleUser struct {
		ID      int64  `json:"id"`
		Email   string `json:"email"`
		Deleted bool   `json:"deleted"`
	}

	result := make([]SimpleUser, len(users))
	for i, u := range users {
		result[i] = SimpleUser{
			ID:      u.ID,
			Email:   u.Email,
			Deleted: u.DeletedAt != nil,
		}
	}

	response.Success(c, result)
}

// SearchAPIKeys handles searching API keys by user
// GET /api/v1/admin/usage/search-api-keys
func (h *UsageHandler) SearchAPIKeys(c *gin.Context) {
	userIDStr := c.Query("user_id")
	keyword := c.Query("q")

	var userID int64
	if userIDStr != "" {
		id, err := strconv.ParseInt(userIDStr, 10, 64)
		if err != nil {
			response.BadRequest(c, "Invalid user_id")
			return
		}
		userID = id
	}

	keys, err := h.apiKeyService.SearchAPIKeys(c.Request.Context(), userID, keyword, 30)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	// Return simplified API key list (only id and name)
	type SimpleAPIKey struct {
		ID     int64  `json:"id"`
		Name   string `json:"name"`
		UserID int64  `json:"user_id"`
	}

	result := make([]SimpleAPIKey, len(keys))
	for i, k := range keys {
		result[i] = SimpleAPIKey{
			ID:     k.ID,
			Name:   k.Name,
			UserID: k.UserID,
		}
	}

	response.Success(c, result)
}

// ListCleanupTasks handles listing usage cleanup tasks
// GET /api/v1/admin/usage/cleanup-tasks
func (h *UsageHandler) ListCleanupTasks(c *gin.Context) {
	if h.cleanupService == nil {
		response.Error(c, http.StatusServiceUnavailable, "Usage cleanup service unavailable")
		return
	}
	operator := int64(0)
	if subject, ok := middleware.GetAuthSubjectFromContext(c); ok {
		operator = subject.UserID
	}
	page, pageSize := response.ParsePagination(c)
	logger.LegacyPrintf("handler.admin.usage", "[UsageCleanup] 请求清理任务列表: operator=%d page=%d page_size=%d", operator, page, pageSize)
	params := pagination.PaginationParams{Page: page, PageSize: pageSize}
	tasks, result, err := h.cleanupService.ListTasks(c.Request.Context(), params)
	if err != nil {
		logger.LegacyPrintf("handler.admin.usage", "[UsageCleanup] 查询清理任务列表失败: operator=%d page=%d page_size=%d err=%v", operator, page, pageSize, err)
		response.ErrorFrom(c, err)
		return
	}
	out := make([]dto.UsageCleanupTask, 0, len(tasks))
	for i := range tasks {
		out = append(out, *dto.UsageCleanupTaskFromService(&tasks[i]))
	}
	logger.LegacyPrintf("handler.admin.usage", "[UsageCleanup] 返回清理任务列表: operator=%d total=%d items=%d page=%d page_size=%d", operator, result.Total, len(out), page, pageSize)
	response.Paginated(c, out, result.Total, page, pageSize)
}

// CreateCleanupTask handles creating a usage cleanup task
// POST /api/v1/admin/usage/cleanup-tasks
func (h *UsageHandler) CreateCleanupTask(c *gin.Context) {
	if h.cleanupService == nil {
		response.Error(c, http.StatusServiceUnavailable, "Usage cleanup service unavailable")
		return
	}
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID <= 0 {
		response.Unauthorized(c, "Unauthorized")
		return
	}

	var req CreateUsageCleanupTaskRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	req.StartDate = strings.TrimSpace(req.StartDate)
	req.EndDate = strings.TrimSpace(req.EndDate)
	if req.StartDate == "" || req.EndDate == "" {
		response.BadRequest(c, "start_date and end_date are required")
		return
	}

	startTime, err := timezone.ParseInUserLocation("2006-01-02", req.StartDate, req.Timezone)
	if err != nil {
		response.BadRequest(c, "Invalid start_date format, use YYYY-MM-DD")
		return
	}
	endTime, err := timezone.ParseInUserLocation("2006-01-02", req.EndDate, req.Timezone)
	if err != nil {
		response.BadRequest(c, "Invalid end_date format, use YYYY-MM-DD")
		return
	}
	endTime = endTime.Add(24*time.Hour - time.Nanosecond)

	var requestType *int16
	stream := req.Stream
	if req.RequestType != nil {
		parsed, err := service.ParseUsageRequestType(*req.RequestType)
		if err != nil {
			response.BadRequest(c, err.Error())
			return
		}
		value := int16(parsed)
		requestType = &value
		stream = nil
	}

	filters := service.UsageCleanupFilters{
		StartTime:   startTime,
		EndTime:     endTime,
		UserID:      req.UserID,
		APIKeyID:    req.APIKeyID,
		AccountID:   req.AccountID,
		GroupID:     req.GroupID,
		Model:       req.Model,
		RequestType: requestType,
		Stream:      stream,
		BillingType: req.BillingType,
	}

	var userID any
	if filters.UserID != nil {
		userID = *filters.UserID
	}
	var apiKeyID any
	if filters.APIKeyID != nil {
		apiKeyID = *filters.APIKeyID
	}
	var accountID any
	if filters.AccountID != nil {
		accountID = *filters.AccountID
	}
	var groupID any
	if filters.GroupID != nil {
		groupID = *filters.GroupID
	}
	var model any
	if filters.Model != nil {
		model = *filters.Model
	}
	var streamValue any
	if filters.Stream != nil {
		streamValue = *filters.Stream
	}
	var requestTypeName any
	if filters.RequestType != nil {
		requestTypeName = service.RequestTypeFromInt16(*filters.RequestType).String()
	}
	var billingType any
	if filters.BillingType != nil {
		billingType = *filters.BillingType
	}

	idempotencyPayload := struct {
		OperatorID int64                         `json:"operator_id"`
		Body       CreateUsageCleanupTaskRequest `json:"body"`
	}{
		OperatorID: subject.UserID,
		Body:       req,
	}
	executeAdminIdempotentJSON(c, "admin.usage.cleanup_tasks.create", idempotencyPayload, service.DefaultWriteIdempotencyTTL(), func(ctx context.Context) (any, error) {
		logger.LegacyPrintf("handler.admin.usage", "[UsageCleanup] 请求创建清理任务: operator=%d start=%s end=%s user_id=%v api_key_id=%v account_id=%v group_id=%v model=%v request_type=%v stream=%v billing_type=%v tz=%q",
			subject.UserID,
			filters.StartTime.Format(time.RFC3339),
			filters.EndTime.Format(time.RFC3339),
			userID,
			apiKeyID,
			accountID,
			groupID,
			model,
			requestTypeName,
			streamValue,
			billingType,
			req.Timezone,
		)

		task, err := h.cleanupService.CreateTask(ctx, filters, subject.UserID)
		if err != nil {
			logger.LegacyPrintf("handler.admin.usage", "[UsageCleanup] 创建清理任务失败: operator=%d err=%v", subject.UserID, err)
			return nil, err
		}
		logger.LegacyPrintf("handler.admin.usage", "[UsageCleanup] 清理任务已创建: task=%d operator=%d status=%s", task.ID, subject.UserID, task.Status)
		return dto.UsageCleanupTaskFromService(task), nil
	})
}

// CancelCleanupTask handles canceling a usage cleanup task
// POST /api/v1/admin/usage/cleanup-tasks/:id/cancel
func (h *UsageHandler) CancelCleanupTask(c *gin.Context) {
	if h.cleanupService == nil {
		response.Error(c, http.StatusServiceUnavailable, "Usage cleanup service unavailable")
		return
	}
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID <= 0 {
		response.Unauthorized(c, "Unauthorized")
		return
	}
	idStr := strings.TrimSpace(c.Param("id"))
	taskID, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil || taskID <= 0 {
		response.BadRequest(c, "Invalid task id")
		return
	}
	logger.LegacyPrintf("handler.admin.usage", "[UsageCleanup] 请求取消清理任务: task=%d operator=%d", taskID, subject.UserID)
	if err := h.cleanupService.CancelTask(c.Request.Context(), taskID, subject.UserID); err != nil {
		logger.LegacyPrintf("handler.admin.usage", "[UsageCleanup] 取消清理任务失败: task=%d operator=%d err=%v", taskID, subject.UserID, err)
		response.ErrorFrom(c, err)
		return
	}
	logger.LegacyPrintf("handler.admin.usage", "[UsageCleanup] 清理任务已取消: task=%d operator=%d", taskID, subject.UserID)
	response.Success(c, gin.H{"id": taskID, "status": service.UsageCleanupStatusCanceled})
}

// parseLatencyCompensationWindow parses the "from"/"to" RFC3339 query
// parameters shared by the preview and apply endpoints. Day-only pickers
// (used elsewhere in the dashboard) aren't precise enough here — an incident
// window is often a few hours, not a whole day — so this takes full
// timestamps and defaults to "since the start of today" when omitted.
func parseLatencyCompensationWindow(c *gin.Context) (from, to time.Time, err error) {
	now := time.Now()
	from = time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	to = now

	if raw := strings.TrimSpace(c.Query("from")); raw != "" {
		from, err = time.Parse(time.RFC3339, raw)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("invalid from: %w", err)
		}
	}
	if raw := strings.TrimSpace(c.Query("to")); raw != "" {
		to, err = time.Parse(time.RFC3339, raw)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("invalid to: %w", err)
		}
	}
	if !to.After(from) {
		return time.Time{}, time.Time{}, fmt.Errorf("to must be after from")
	}
	return from, to, nil
}

// PreviewLatencyCompensation reports what a latency-compensation payout over
// the given window would look like — per user, how much was actually
// charged for slow requests versus what they cost the platform — without
// crediting anyone. GET so an admin can re-run it freely while narrowing the
// date range.
func (h *UsageHandler) PreviewLatencyCompensation(c *gin.Context) {
	from, to, err := parseLatencyCompensationWindow(c)
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	thresholdMs, err := strconv.Atoi(strings.TrimSpace(c.Query("threshold_ms")))
	if err != nil || thresholdMs <= 0 {
		response.BadRequest(c, "threshold_ms must be a positive integer")
		return
	}
	profitRatio, err := parseLatencyCompensationProfitRatio(c.Query("profit_ratio"))
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	summary, err := h.usageService.PreviewLatencyCompensation(c.Request.Context(), from, to, thresholdMs, profitRatio)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, summary)
}

// parseLatencyCompensationProfitRatio parses the "how much of the margin to
// refund" ratio (0~1). An empty string defaults to 1 (refund the full
// margin) so callers that don't care about partial refunds don't have to
// pass it.
func parseLatencyCompensationProfitRatio(raw string) (float64, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 1, nil
	}
	ratio, err := strconv.ParseFloat(raw, 64)
	if err != nil || ratio < 0 || ratio > 1 {
		return 0, fmt.Errorf("profit_ratio must be a number between 0 and 1")
	}
	return ratio, nil
}

// ApplyLatencyCompensationRequest is the POST body for actually paying out a
// latency compensation batch.
type ApplyLatencyCompensationRequest struct {
	From        string   `json:"from" binding:"required"`
	To          string   `json:"to" binding:"required"`
	ThresholdMs int      `json:"threshold_ms" binding:"required,min=1"`
	ProfitRatio *float64 `json:"profit_ratio"`
}

// ApplyLatencyCompensation credits every user's margin (actual_cost minus
// account_cost) on qualifying slow requests in the window, then marks
// exactly those requests compensated.
//
// Rows are fetched once up front and both the payout and the marking use
// that same row set — not a fresh "WHERE first_token_ms >= threshold"
// re-query for the mark step — because a request that lands in the window
// between the fetch and the mark would otherwise get silently marked
// compensated without ever having been paid for.
//
// Per-user crediting goes through AdminService.UpdateUserBalance, the same
// path the manual admin balance adjustment uses, so cache invalidation and
// the redeem_codes audit trail behave identically to a human doing this one
// user at a time in the admin panel.
func (h *UsageHandler) ApplyLatencyCompensation(c *gin.Context) {
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID <= 0 {
		response.Unauthorized(c, "Unauthorized")
		return
	}

	var req ApplyLatencyCompensationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	from, err := time.Parse(time.RFC3339, req.From)
	if err != nil {
		response.BadRequest(c, "invalid from: "+err.Error())
		return
	}
	to, err := time.Parse(time.RFC3339, req.To)
	if err != nil {
		response.BadRequest(c, "invalid to: "+err.Error())
		return
	}
	if !to.After(from) {
		response.BadRequest(c, "to must be after from")
		return
	}
	profitRatio := 1.0
	if req.ProfitRatio != nil {
		if *req.ProfitRatio < 0 || *req.ProfitRatio > 1 {
			response.BadRequest(c, "profit_ratio must be between 0 and 1")
			return
		}
		profitRatio = *req.ProfitRatio
	}

	ctx := c.Request.Context()
	rows, err := h.usageService.FetchPendingLatencyCompensationRows(ctx, from, to, req.ThresholdMs)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	summary := service.SummarizeLatencyCompensationRows(rows, from, to, req.ThresholdMs, profitRatio)

	ids := make([]int64, len(rows))
	for i, row := range rows {
		ids[i] = row.ID
	}

	paidUsers := 0
	for _, u := range summary.Users {
		if u.Compensation <= 0 {
			continue
		}
		// notes 会原样出现在用户自己的余额/充值记录里，是用户唯一能看到的解释，
		// 所以就用这四个字打头，不堆技术细节；日期区间留着方便用户对应是哪天变慢了。
		// 这串文本同时是撤回时用来定位、删除这条记录的匹配 key，见
		// RevokeLatencyCompensation 和 latencyCompensationGrantNotes。
		notes := latencyCompensationGrantNotes(from, to)
		if _, err := h.adminService.UpdateUserBalance(ctx, u.UserID, u.Compensation, "add", notes); err != nil {
			logger.LegacyPrintf("handler.admin.usage",
				"[LatencyCompensation] 补偿到账失败，本次未标记为已补偿: user=%d amount=%.8f err=%v",
				u.UserID, u.Compensation, err)
			response.ErrorFrom(c, fmt.Errorf("credited %d of %d users before failing on user %d: %w", paidUsers, len(summary.Users), u.UserID, err))
			return
		}
		paidUsers++
	}

	if err := h.usageService.MarkLatencyCompensated(ctx, ids); err != nil {
		// Balances are already credited at this point; failing to mark just
		// means the same rows could be picked up (and paid again) by the next
		// apply over an overlapping window — log loudly so an admin notices
		// before that happens, rather than silently losing the failure.
		logger.LegacyPrintf("handler.admin.usage",
			"[LatencyCompensation] 已补偿 %d 位用户但标记 %d 条请求为已补偿失败，存在重复补偿风险: err=%v",
			paidUsers, len(ids), err)
		response.ErrorFrom(c, fmt.Errorf("paid %d users but failed to mark rows compensated (retrying will double-pay): %w", paidUsers, err))
		return
	}

	logger.LegacyPrintf("handler.admin.usage",
		"[LatencyCompensation] 补偿发放完成: operator=%d users=%d requests=%d total=%.8f",
		subject.UserID, paidUsers, len(ids), summary.TotalCompensation)
	response.Success(c, summary)
}

// latencyCompensationGrantNotes builds the exact notes text a latency-
// compensation grant over [from, to) stores via UpdateUserBalance — the only
// explanation of "+X" the user ever sees, and also the lookup key
// RevokeLatencyCompensation uses to find and delete that same audit row.
func latencyCompensationGrantNotes(from, to time.Time) string {
	return fmt.Sprintf("流量延迟补偿：%s ~ %s", from.Format("2006-01-02 15:04"), to.Format("2006-01-02 15:04"))
}

// legacyLatencyCompensationGrantNotes reproduces the notes text grants made
// before 2026-09-09 actually stored: the layout string used "2026-01-02
// 15:04" instead of Go's real reference year "2006", so Format() didn't
// recognize a year token there and rendered garbled years (e.g.
// "7076-09-07 16:00"). Revoking one of those older grants must still match
// this exact garbled text to find and delete it — this exists only for that
// fallback lookup, never for new writes.
func legacyLatencyCompensationGrantNotes(from, to time.Time) string {
	return fmt.Sprintf("流量延迟补偿：%s ~ %s", from.Format("2026-01-02 15:04"), to.Format("2026-01-02 15:04"))
}

// RevokeLatencyCompensationUser is one line of the per-user amount to claw
// back — the caller (TransitHub) already has this from the payout record it
// stored when the compensation was originally applied.
type RevokeLatencyCompensationUser struct {
	UserID int64   `json:"user_id" binding:"required"`
	Amount float64 `json:"amount" binding:"required,gt=0"`
}

// RevokeLatencyCompensationRequest identifies the original payout to undo:
// the same window/threshold that was compensated, plus who got how much.
type RevokeLatencyCompensationRequest struct {
	From        string                           `json:"from" binding:"required"`
	To          string                           `json:"to" binding:"required"`
	ThresholdMs int                              `json:"threshold_ms" binding:"required,min=1"`
	Users       []RevokeLatencyCompensationUser `json:"users" binding:"required,min=1"`
}

// RevokeLatencyCompensationResult reports what actually happened per user —
// a revoke can partially fail (a user already spent the credited balance),
// and the caller needs to know which ones so it doesn't silently claim
// "fully undone" when it wasn't.
type RevokeLatencyCompensationResult struct {
	RevokedUsers []int64 `json:"revoked_users"`
	SkippedUsers []int64 `json:"skipped_users"`
	TotalRevoked float64 `json:"total_revoked"`
}

// RevokeLatencyCompensation undoes a mistaken payout: subtracts each user's
// original amount back off their balance and reopens the underlying
// usage_logs rows (clears latency_compensated_at) so a corrected re-run can
// compensate them properly. Deliberately does NOT go through
// AdminService.UpdateUserBalance — the balance change here has to leave no
// redeem_codes trail, because a "+补偿 / -补偿" pair on the user's own
// balance history reads as a billing mistake and generates support tickets,
// when this is actually the operator correcting their own mistake before
// the user was ever meant to see it.
//
// A user whose balance has since dropped below the amount to claw back is
// skipped rather than forced negative — reported in skipped_users so the
// caller (and the operator) knows the revoke was only partial.
func (h *UsageHandler) RevokeLatencyCompensation(c *gin.Context) {
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID <= 0 {
		response.Unauthorized(c, "Unauthorized")
		return
	}

	var req RevokeLatencyCompensationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	from, err := time.Parse(time.RFC3339, req.From)
	if err != nil {
		response.BadRequest(c, "invalid from: "+err.Error())
		return
	}
	to, err := time.Parse(time.RFC3339, req.To)
	if err != nil {
		response.BadRequest(c, "invalid to: "+err.Error())
		return
	}
	if !to.After(from) {
		response.BadRequest(c, "to must be after from")
		return
	}

	ctx := c.Request.Context()
	result := RevokeLatencyCompensationResult{
		RevokedUsers: make([]int64, 0, len(req.Users)),
		SkippedUsers: make([]int64, 0),
	}
	notes := latencyCompensationGrantNotes(from, to)
	legacyNotes := legacyLatencyCompensationGrantNotes(from, to)
	for _, u := range req.Users {
		if _, err := h.adminService.AdjustUserBalanceSilently(ctx, u.UserID, -u.Amount); err != nil {
			logger.LegacyPrintf("handler.admin.usage",
				"[LatencyCompensation] 撤回失败，跳过该用户（可能余额已不足以扣回）: operator=%d user=%d amount=%.8f err=%v",
				subject.UserID, u.UserID, u.Amount, err)
			result.SkippedUsers = append(result.SkippedUsers, u.UserID)
			continue
		}
		result.RevokedUsers = append(result.RevokedUsers, u.UserID)
		result.TotalRevoked += u.Amount

		// 余额已经扣回；接着把原发放那笔 +补偿 的审计行也删掉，不然用户自己的
		// 余额记录里会留一条对不上账的"+X"，看起来像系统白送了一笔钱又不明不白
		// 消失。找不到/删不掉不影响撤回本身是否成功，只响亮记日志。
		found, scrubErr := h.adminService.DeleteAdminAdjustmentTrace(ctx, u.UserID, u.Amount, notes)
		if scrubErr == nil && !found {
			found, scrubErr = h.adminService.DeleteAdminAdjustmentTrace(ctx, u.UserID, u.Amount, legacyNotes)
		}
		if scrubErr != nil {
			logger.LegacyPrintf("handler.admin.usage",
				"[LatencyCompensation] 撤回成功但清除原发放记录出错，用户余额记录里会留一条对不上账的 +补偿: operator=%d user=%d amount=%.8f err=%v",
				subject.UserID, u.UserID, u.Amount, scrubErr)
		} else if !found {
			logger.LegacyPrintf("handler.admin.usage",
				"[LatencyCompensation] 撤回成功但没找到匹配的原发放记录，用户余额记录里会留一条对不上账的 +补偿: operator=%d user=%d amount=%.8f",
				subject.UserID, u.UserID, u.Amount)
		}
	}

	if err := h.usageService.UnmarkLatencyCompensated(ctx, from, to, req.ThresholdMs); err != nil {
		// 余额已经扣回，这一步失败只影响"这批请求能否被重新正确补偿一次"，
		// 不影响撤回本身，所以照样返回成功，但要响亮地记日志。
		logger.LegacyPrintf("handler.admin.usage",
			"[LatencyCompensation] 撤回已扣回 %d 位用户余额，但重新打开慢请求标记失败: operator=%d err=%v",
			len(result.RevokedUsers), subject.UserID, err)
	}

	logger.LegacyPrintf("handler.admin.usage",
		"[LatencyCompensation] 撤回完成: operator=%d revoked=%d skipped=%d total=%.8f",
		subject.UserID, len(result.RevokedUsers), len(result.SkippedUsers), result.TotalRevoked)
	response.Success(c, result)
}
