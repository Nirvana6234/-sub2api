package my_sites

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"transithub/backend/internal/shared/authctx"
	"transithub/backend/internal/shared/httpjson"
)

type Handler struct {
	service *Service
}

// RegisterRoutes 注册分组映射相关的路由。
// 包含映射选项查询、映射关系保存、真实对接创建和记录查询。
func RegisterRoutes(mux *http.ServeMux, service *Service) {
	handler := &Handler{service: service}
	mux.HandleFunc("GET /api/my-sites/mapping-options", handler.mappingOptions)
	mux.HandleFunc("PUT /api/my-sites/mappings", handler.saveMappings)
	mux.HandleFunc("PATCH /api/my-sites/mappings", handler.saveMapping)
	mux.HandleFunc("DELETE /api/my-sites/mappings/{ownGroup}", handler.removeMapping)
	mux.HandleFunc("POST /api/my-sites/auto-pricing/run", handler.runAutoPricing)
	mux.HandleFunc("POST /api/my-sites/real-connect", handler.realConnect)
	mux.HandleFunc("POST /api/my-sites/real-bind", handler.realBind)
	mux.HandleFunc("GET /api/my-sites/upstream-keys", handler.listUpstreamKeys)
	mux.HandleFunc("POST /api/my-sites/upstream-keys/test", handler.testUpstreamKey)
	mux.HandleFunc("POST /api/my-sites/upstream-keys/models", handler.listUpstreamKeyModels)
	mux.HandleFunc("GET /api/my-sites/admin-resources", handler.listAdminResources)
	mux.HandleFunc("GET /api/my-sites/real-connections", handler.listRealConnections)
	mux.HandleFunc("POST /api/my-sites/real-disconnect", handler.realDisconnect)
	mux.HandleFunc("GET /api/my-sites/latency-subsidy-tasks", handler.listLatencySubsidyTasks)
	mux.HandleFunc("POST /api/my-sites/latency-subsidy-tasks", handler.createLatencySubsidyTask)
	mux.HandleFunc("PUT /api/my-sites/latency-subsidy-tasks/{id}", handler.updateLatencySubsidyTask)
	mux.HandleFunc("DELETE /api/my-sites/latency-subsidy-tasks/{id}", handler.deleteLatencySubsidyTask)
	mux.HandleFunc("GET /api/my-sites/latency-subsidy-tasks/{id}/preview", handler.previewLatencySubsidyTask)
	mux.HandleFunc("POST /api/my-sites/latency-subsidy-tasks/{id}/apply", handler.applyLatencySubsidyTask)
	mux.HandleFunc("GET /api/my-sites/latency-subsidy-tasks-history", handler.listLatencyCompensationHistory)
	mux.HandleFunc("POST /api/my-sites/latency-subsidy-tasks-history/{id}/revoke", handler.revokeLatencyCompensationPayout)
}

// removeMapping 显式删除一个自有分组的映射配置，主要用于清理已失效分组。
// 删除必须由用户动作触发，mapping-options 查询不会再执行隐式清理。
func (h *Handler) removeMapping(w http.ResponseWriter, r *http.Request) {
	userID, ok := authctx.UserID(r.Context())
	if !ok {
		httpjson.WriteError(w, http.StatusUnauthorized, "auth.errors.unauthorized")
		return
	}
	response, err := h.service.RemoveMapping(r.Context(), userID, r.PathValue("ownGroup"))
	if err != nil {
		writeError(w, err)
		return
	}
	httpjson.Write(w, http.StatusOK, response)
}

// saveMapping 只更新一个自有分组的映射与自动调价配置。
// 旧版客户端仍可使用 PUT 全量保存；新版客户端使用 PATCH，避免并发页面互相覆盖。
func (h *Handler) saveMapping(w http.ResponseWriter, r *http.Request) {
	userID, ok := authctx.UserID(r.Context())
	if !ok {
		httpjson.WriteError(w, http.StatusUnauthorized, "auth.errors.unauthorized")
		return
	}
	var dto struct {
		Mapping MappingRequest `json:"mapping"`
	}
	if err := httpjson.Decode(r, &dto); err != nil {
		httpjson.WriteError(w, http.StatusBadRequest, ErrorRequest)
		return
	}
	response, err := h.service.SaveMapping(r.Context(), userID, dto.Mapping)
	if err != nil {
		writeError(w, err)
		return
	}
	httpjson.Write(w, http.StatusOK, response)
}

func (h *Handler) mappingOptions(w http.ResponseWriter, r *http.Request) {
	userID, ok := authctx.UserID(r.Context())
	if !ok {
		httpjson.WriteError(w, http.StatusUnauthorized, "auth.errors.unauthorized")
		return
	}
	response, err := h.service.MappingOptions(r.Context(), userID)
	if err != nil {
		writeError(w, err)
		return
	}
	httpjson.Write(w, http.StatusOK, response)
}

func (h *Handler) saveMappings(w http.ResponseWriter, r *http.Request) {
	userID, ok := authctx.UserID(r.Context())
	if !ok {
		httpjson.WriteError(w, http.StatusUnauthorized, "auth.errors.unauthorized")
		return
	}
	var dto struct {
		Mappings []MappingRequest `json:"mappings"`
	}
	if err := httpjson.Decode(r, &dto); err != nil {
		httpjson.WriteError(w, http.StatusBadRequest, ErrorRequest)
		return
	}
	response, err := h.service.SaveMappings(r.Context(), userID, dto.Mappings)
	if err != nil {
		writeError(w, err)
		return
	}
	httpjson.Write(w, http.StatusOK, response)
}

func (h *Handler) runAutoPricing(w http.ResponseWriter, r *http.Request) {
	userID, ok := authctx.UserID(r.Context())
	if !ok {
		httpjson.WriteError(w, http.StatusUnauthorized, "auth.errors.unauthorized")
		return
	}
	var req AutoPricingRunRequest
	if err := httpjson.Decode(r, &req); err != nil {
		httpjson.WriteError(w, http.StatusBadRequest, ErrorRequest)
		return
	}
	response, err := h.service.RunAutoPricingNow(r.Context(), userID, req)
	if err != nil {
		writeError(w, err)
		return
	}
	httpjson.Write(w, http.StatusOK, response)
}

// realConnect 真实对接：在上游站点创建 key，在 admin 站点创建转发账号。
func (h *Handler) realConnect(w http.ResponseWriter, r *http.Request) {
	userID, ok := authctx.UserID(r.Context())
	if !ok {
		httpjson.WriteError(w, http.StatusUnauthorized, "auth.errors.unauthorized")
		return
	}
	var req RealConnectRequest
	if err := httpjson.Decode(r, &req); err != nil {
		httpjson.WriteError(w, http.StatusBadRequest, ErrorRequest)
		return
	}
	response, err := h.service.RealConnect(r.Context(), userID, req)
	if err != nil {
		writeError(w, err)
		return
	}
	httpjson.Write(w, http.StatusOK, response)
}

// listUpstreamKeys 获取指定上游站点的 API Key 列表，供手动绑定时选择。
func (h *Handler) listUpstreamKeys(w http.ResponseWriter, r *http.Request) {
	userID, ok := authctx.UserID(r.Context())
	if !ok {
		httpjson.WriteError(w, http.StatusUnauthorized, "auth.errors.unauthorized")
		return
	}
	siteID := r.URL.Query().Get("siteId")
	if siteID == "" {
		httpjson.WriteError(w, http.StatusBadRequest, ErrorRequest)
		return
	}
	keys, err := h.service.ListUpstreamCredentials(
		r.Context(),
		userID,
		siteID,
		r.URL.Query().Get("groupId"),
		r.URL.Query().Get("groupName"),
	)
	if err != nil {
		writeError(w, err)
		return
	}
	httpjson.Write(w, http.StatusOK, keys)
}

// testUpstreamKey 对一个上游 Key 做连通性测试：先列模型，再发一次最小请求。
//
// 用 POST 而不是 GET：它会真实打到上游并产生（极小的）计费，不该被当成
// 可以随便重放的幂等读操作。
func (h *Handler) testUpstreamKey(w http.ResponseWriter, r *http.Request) {
	userID, ok := authctx.UserID(r.Context())
	if !ok {
		httpjson.WriteError(w, http.StatusUnauthorized, "auth.errors.unauthorized")
		return
	}
	var request UpstreamKeyTestRequest
	if err := httpjson.Decode(r, &request); err != nil {
		httpjson.WriteError(w, http.StatusBadRequest, ErrorRequest)
		return
	}
	if strings.TrimSpace(request.UpstreamSiteID) == "" {
		httpjson.WriteError(w, http.StatusBadRequest, ErrorRequest)
		return
	}
	response, err := h.service.TestUpstreamCredential(r.Context(), userID, request)
	if err != nil {
		writeError(w, err)
		return
	}
	httpjson.Write(w, http.StatusOK, response)
}

func (h *Handler) listUpstreamKeyModels(w http.ResponseWriter, r *http.Request) {
	userID, ok := authctx.UserID(r.Context())
	if !ok {
		httpjson.WriteError(w, http.StatusUnauthorized, "auth.errors.unauthorized")
		return
	}
	var request UpstreamKeyTestRequest
	if err := httpjson.Decode(r, &request); err != nil || strings.TrimSpace(request.UpstreamSiteID) == "" {
		httpjson.WriteError(w, http.StatusBadRequest, ErrorRequest)
		return
	}
	response, err := h.service.ListUpstreamCredentialModels(r.Context(), userID, request)
	if err != nil {
		writeError(w, err)
		return
	}
	httpjson.Write(w, http.StatusOK, response)
}

// listAdminResources returns existing accounts/channels from one group on the
// current admin site. The service repeats this lookup during binding so stale or
// forged browser selections cannot create a local connection record.
func (h *Handler) listAdminResources(w http.ResponseWriter, r *http.Request) {
	userID, ok := authctx.UserID(r.Context())
	if !ok {
		httpjson.WriteError(w, http.StatusUnauthorized, "auth.errors.unauthorized")
		return
	}
	groupID := r.URL.Query().Get("groupId")
	if groupID == "" {
		httpjson.WriteError(w, http.StatusBadRequest, ErrorRequest)
		return
	}
	resources, err := h.service.ListAdminResources(r.Context(), userID, groupID)
	if err != nil {
		writeError(w, err)
		return
	}
	if resources == nil {
		resources = []AdminResourceOption{}
	}
	httpjson.Write(w, http.StatusOK, resources)
}

// realBind 手动绑定已有的上游 Key，仅创建绑定记录。
func (h *Handler) realBind(w http.ResponseWriter, r *http.Request) {
	userID, ok := authctx.UserID(r.Context())
	if !ok {
		httpjson.WriteError(w, http.StatusUnauthorized, "auth.errors.unauthorized")
		return
	}
	var req RealBindRequest
	if err := httpjson.Decode(r, &req); err != nil {
		httpjson.WriteError(w, http.StatusBadRequest, ErrorRequest)
		return
	}
	response, err := h.service.RealBind(r.Context(), userID, req)
	if err != nil {
		writeError(w, err)
		return
	}
	httpjson.Write(w, http.StatusOK, response)
}

// listRealConnections 查询当前用户的所有真实对接绑定记录。
func (h *Handler) listRealConnections(w http.ResponseWriter, r *http.Request) {
	userID, ok := authctx.UserID(r.Context())
	if !ok {
		httpjson.WriteError(w, http.StatusUnauthorized, "auth.errors.unauthorized")
		return
	}
	connections, err := h.service.ListRealConnections(r.Context(), userID)
	if err != nil {
		writeError(w, err)
		return
	}
	if connections == nil {
		connections = []RealConnection{}
	}
	httpjson.Write(w, http.StatusOK, connections)
}

// realDisconnect 取消真实对接：删除记录，可选同时删除上游 key 和 admin 账号。
func (h *Handler) realDisconnect(w http.ResponseWriter, r *http.Request) {
	userID, ok := authctx.UserID(r.Context())
	if !ok {
		httpjson.WriteError(w, http.StatusUnauthorized, "auth.errors.unauthorized")
		return
	}
	var req RealDisconnectRequest
	if err := httpjson.Decode(r, &req); err != nil {
		httpjson.WriteError(w, http.StatusBadRequest, ErrorRequest)
		return
	}
	if err := h.service.RealDisconnect(r.Context(), userID, req); err != nil {
		writeError(w, err)
		return
	}
	httpjson.Write(w, http.StatusOK, map[string]bool{"ok": true})
}

// listLatencySubsidyTasks lists every 延迟补贴任务 for the caller's current workspace.
func (h *Handler) listLatencySubsidyTasks(w http.ResponseWriter, r *http.Request) {
	userID, ok := authctx.UserID(r.Context())
	if !ok {
		httpjson.WriteError(w, http.StatusUnauthorized, "auth.errors.unauthorized")
		return
	}
	tasks, err := h.service.ListLatencySubsidyTasks(r.Context(), userID)
	if err != nil {
		writeError(w, err)
		return
	}
	views := make([]LatencySubsidyTaskView, 0, len(tasks))
	for _, t := range tasks {
		views = append(views, toLatencySubsidyTaskView(t))
	}
	httpjson.Write(w, http.StatusOK, views)
}

// createLatencySubsidyTask saves a new task. Creating it does not run
// anything — running is a separate, explicit action.
func (h *Handler) createLatencySubsidyTask(w http.ResponseWriter, r *http.Request) {
	userID, ok := authctx.UserID(r.Context())
	if !ok {
		httpjson.WriteError(w, http.StatusUnauthorized, "auth.errors.unauthorized")
		return
	}
	var req LatencySubsidyTaskInput
	if err := httpjson.Decode(r, &req); err != nil {
		httpjson.WriteError(w, http.StatusBadRequest, ErrorRequest)
		return
	}
	task, err := h.service.CreateLatencySubsidyTask(r.Context(), userID, req)
	if err != nil {
		writeError(w, err)
		return
	}
	httpjson.Write(w, http.StatusOK, toLatencySubsidyTaskView(task))
}

// updateLatencySubsidyTask replaces a task's config in place (same id).
func (h *Handler) updateLatencySubsidyTask(w http.ResponseWriter, r *http.Request) {
	userID, ok := authctx.UserID(r.Context())
	if !ok {
		httpjson.WriteError(w, http.StatusUnauthorized, "auth.errors.unauthorized")
		return
	}
	taskID := r.PathValue("id")
	var req LatencySubsidyTaskInput
	if err := httpjson.Decode(r, &req); err != nil {
		httpjson.WriteError(w, http.StatusBadRequest, ErrorRequest)
		return
	}
	task, err := h.service.UpdateLatencySubsidyTask(r.Context(), userID, taskID, req)
	if err != nil {
		writeError(w, err)
		return
	}
	httpjson.Write(w, http.StatusOK, toLatencySubsidyTaskView(task))
}

// deleteLatencySubsidyTask removes a task. Past payouts it triggered stay in
// history under the name the task had at the time.
func (h *Handler) deleteLatencySubsidyTask(w http.ResponseWriter, r *http.Request) {
	userID, ok := authctx.UserID(r.Context())
	if !ok {
		httpjson.WriteError(w, http.StatusUnauthorized, "auth.errors.unauthorized")
		return
	}
	taskID := r.PathValue("id")
	if err := h.service.DeleteLatencySubsidyTask(r.Context(), userID, taskID); err != nil {
		writeError(w, err)
		return
	}
	httpjson.Write(w, http.StatusOK, map[string]bool{"ok": true})
}

// parseTimeWindow parses "from"/"to" RFC3339 query params, shared by the
// task preview endpoint. Unlike the old ad-hoc preview, threshold/ratio are
// no longer caller-supplied here — they come from the task itself.
func parseTimeWindow(from, to string) (time.Time, time.Time, error) {
	fromTime, err := time.Parse(time.RFC3339, strings.TrimSpace(from))
	if err != nil {
		return time.Time{}, time.Time{}, errors.New("invalid from")
	}
	toTime, err := time.Parse(time.RFC3339, strings.TrimSpace(to))
	if err != nil {
		return time.Time{}, time.Time{}, errors.New("invalid to")
	}
	if !toTime.After(fromTime) {
		return time.Time{}, time.Time{}, errors.New("to must be after from")
	}
	return fromTime, toTime, nil
}

// previewLatencySubsidyTask reports what running this task over [from, to)
// would pay out, without crediting anyone.
func (h *Handler) previewLatencySubsidyTask(w http.ResponseWriter, r *http.Request) {
	userID, ok := authctx.UserID(r.Context())
	if !ok {
		httpjson.WriteError(w, http.StatusUnauthorized, "auth.errors.unauthorized")
		return
	}
	from, to, err := parseTimeWindow(r.URL.Query().Get("from"), r.URL.Query().Get("to"))
	if err != nil {
		httpjson.WriteError(w, http.StatusBadRequest, ErrorRequest)
		return
	}
	taskID := r.PathValue("id")
	summary, _, err := h.service.RunLatencySubsidyTaskPreview(r.Context(), userID, taskID, from, to)
	if err != nil {
		writeError(w, err)
		return
	}
	httpjson.Write(w, http.StatusOK, toLatencyCompensationSummary(summary))
}

// RunLatencySubsidyTaskRequest is the POST body for actually running a task.
type RunLatencySubsidyTaskRequest struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// applyLatencySubsidyTask actually pays out this task's compensation over
// [from, to). Irreversible — the frontend must have shown the operator a
// preview and gotten explicit confirmation before calling this.
func (h *Handler) applyLatencySubsidyTask(w http.ResponseWriter, r *http.Request) {
	userID, ok := authctx.UserID(r.Context())
	if !ok {
		httpjson.WriteError(w, http.StatusUnauthorized, "auth.errors.unauthorized")
		return
	}
	var req RunLatencySubsidyTaskRequest
	if err := httpjson.Decode(r, &req); err != nil {
		httpjson.WriteError(w, http.StatusBadRequest, ErrorRequest)
		return
	}
	from, to, err := parseTimeWindow(req.From, req.To)
	if err != nil {
		httpjson.WriteError(w, http.StatusBadRequest, ErrorRequest)
		return
	}
	taskID := r.PathValue("id")
	summary, err := h.service.RunLatencySubsidyTaskApply(r.Context(), userID, taskID, from, to)
	if err != nil {
		writeError(w, err)
		return
	}
	httpjson.Write(w, http.StatusOK, toLatencyCompensationSummary(summary))
}

// listLatencyCompensationHistory returns past payout batches (newest
// first), each with its full per-user breakdown, so an operator can see
// what was already refunded and when without digging through raw records.
func (h *Handler) listLatencyCompensationHistory(w http.ResponseWriter, r *http.Request) {
	userID, ok := authctx.UserID(r.Context())
	if !ok {
		httpjson.WriteError(w, http.StatusUnauthorized, "auth.errors.unauthorized")
		return
	}
	limit := 50
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		if parsed, parseErr := strconv.Atoi(raw); parseErr == nil && parsed > 0 {
			limit = parsed
		}
	}
	payouts, err := h.service.ListLatencyCompensationPayouts(r.Context(), userID, limit)
	if err != nil {
		writeError(w, err)
		return
	}
	views := make([]LatencyCompensationPayoutView, 0, len(payouts))
	for _, p := range payouts {
		views = append(views, toLatencyCompensationPayoutView(p))
	}
	httpjson.Write(w, http.StatusOK, views)
}

// RevokeLatencyCompensationPayoutResponse is the wire shape for a revoke
// result — camelCase, mirroring upstream.RevokeLatencyCompensationResult.
type RevokeLatencyCompensationPayoutResponse struct {
	RevokedUserIDs []int64 `json:"revokedUserIds"`
	SkippedUserIDs []int64 `json:"skippedUserIds"`
	TotalRevoked   float64 `json:"totalRevoked"`
}

// revokeLatencyCompensationPayout undoes an entire past payout batch — every
// user in it. Irreversible in the other direction (there's no "un-revoke");
// the frontend must confirm with the operator before calling this.
func (h *Handler) revokeLatencyCompensationPayout(w http.ResponseWriter, r *http.Request) {
	userID, ok := authctx.UserID(r.Context())
	if !ok {
		httpjson.WriteError(w, http.StatusUnauthorized, "auth.errors.unauthorized")
		return
	}
	payoutID := r.PathValue("id")
	result, err := h.service.RevokeLatencyCompensationPayout(r.Context(), userID, payoutID)
	if err != nil {
		writeError(w, err)
		return
	}
	httpjson.Write(w, http.StatusOK, RevokeLatencyCompensationPayoutResponse{
		RevokedUserIDs: result.RevokedUserIDs,
		SkippedUserIDs: result.SkippedUserIDs,
		TotalRevoked:   result.TotalRevoked,
	})
}

func writeError(w http.ResponseWriter, err error) {
	var requestErr requestError
	if errors.As(err, &requestErr) {
		status := http.StatusBadRequest
		if requestErr == requestError(ErrorAuthRequired) {
			status = http.StatusUnauthorized
		}
		if requestErr == requestError(ErrorAdminOnly) {
			status = http.StatusForbidden
		}
		if requestErr == requestError("admin.adminAccounts.errors.noCurrentAccount") {
			status = http.StatusConflict
		}
		if requestErr == requestError(ErrorConnectionExists) || requestErr == requestError(ErrorManagedDeleteOnly) {
			status = http.StatusConflict
		}
		httpjson.WriteError(w, status, requestErr.Error())
		return
	}
	httpjson.WriteError(w, http.StatusInternalServerError, ErrorUnknown)
}
