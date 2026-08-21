package purity_check

import (
	"errors"
	"io"
	"net/http"
	"strconv"

	"transithub/backend/internal/shared/authctx"
	"transithub/backend/internal/shared/httpjson"
)

type Handler struct {
	service *Service
}

// RegisterRoutes 注册纯度检测模块的全部路由。
// 响应体一律不含上游 API key——这个模块的整条链路上，明文凭据只在 worker 的
// 栈上出现过一次（resolveJobCredential → DetectorClient.Start）。
func RegisterRoutes(mux *http.ServeMux, service *Service) {
	handler := &Handler{service: service}
	mux.HandleFunc("GET /api/purity-check/targets", handler.targets)
	mux.HandleFunc("GET /api/purity-check/tiers", handler.tiers)
	mux.HandleFunc("GET /api/purity-check/jobs", handler.listJobs)
	mux.HandleFunc("POST /api/purity-check/jobs", handler.submit)
	mux.HandleFunc("GET /api/purity-check/jobs/{id}", handler.getJob)
	mux.HandleFunc("POST /api/purity-check/jobs/{id}/cancel", handler.cancel)
	mux.HandleFunc("DELETE /api/purity-check/jobs/{id}", handler.delete)
}

func (h *Handler) targets(w http.ResponseWriter, r *http.Request) {
	userID, ok := authctx.UserID(r.Context())
	if !ok {
		httpjson.WriteError(w, http.StatusUnauthorized, "auth.errors.unauthorized")
		return
	}
	targets, err := h.service.ListTargets(r.Context(), userID)
	if err != nil {
		writeError(w, err)
		return
	}
	if targets == nil {
		targets = []Target{}
	}
	httpjson.Write(w, http.StatusOK, targets)
}

func (h *Handler) tiers(w http.ResponseWriter, r *http.Request) {
	userID, ok := authctx.UserID(r.Context())
	if !ok {
		httpjson.WriteError(w, http.StatusUnauthorized, "auth.errors.unauthorized")
		return
	}
	_ = userID
	tiers, err := h.service.TierInfos(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	httpjson.Write(w, http.StatusOK, tiers)
}

func (h *Handler) listJobs(w http.ResponseWriter, r *http.Request) {
	userID, ok := authctx.UserID(r.Context())
	if !ok {
		httpjson.WriteError(w, http.StatusUnauthorized, "auth.errors.unauthorized")
		return
	}
	limit := 0
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil {
			limit = parsed
		}
	}
	response, err := h.service.ListJobs(r.Context(), userID, limit)
	if err != nil {
		writeError(w, err)
		return
	}
	if response.Jobs == nil {
		response.Jobs = []Job{}
	}
	httpjson.Write(w, http.StatusOK, response)
}

func (h *Handler) submit(w http.ResponseWriter, r *http.Request) {
	userID, ok := authctx.UserID(r.Context())
	if !ok {
		httpjson.WriteError(w, http.StatusUnauthorized, "auth.errors.unauthorized")
		return
	}
	var input SubmitInput
	if err := httpjson.Decode(r, &input); err != nil && !errors.Is(err, io.EOF) {
		httpjson.WriteError(w, http.StatusBadRequest, ErrorRequest)
		return
	}
	jobs, err := h.service.Submit(r.Context(), userID, input)
	if err != nil {
		writeError(w, err)
		return
	}
	httpjson.Write(w, http.StatusOK, map[string]any{"jobs": jobs})
}

func (h *Handler) getJob(w http.ResponseWriter, r *http.Request) {
	userID, ok := authctx.UserID(r.Context())
	if !ok {
		httpjson.WriteError(w, http.StatusUnauthorized, "auth.errors.unauthorized")
		return
	}
	detail, err := h.service.GetJob(r.Context(), userID, r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	httpjson.Write(w, http.StatusOK, detail)
}

func (h *Handler) cancel(w http.ResponseWriter, r *http.Request) {
	userID, ok := authctx.UserID(r.Context())
	if !ok {
		httpjson.WriteError(w, http.StatusUnauthorized, "auth.errors.unauthorized")
		return
	}
	if err := h.service.Cancel(r.Context(), userID, r.PathValue("id")); err != nil {
		writeError(w, err)
		return
	}
	httpjson.Write(w, http.StatusOK, map[string]bool{"ok": true})
}

func (h *Handler) delete(w http.ResponseWriter, r *http.Request) {
	userID, ok := authctx.UserID(r.Context())
	if !ok {
		httpjson.WriteError(w, http.StatusUnauthorized, "auth.errors.unauthorized")
		return
	}
	if err := h.service.Delete(r.Context(), userID, r.PathValue("id")); err != nil {
		writeError(w, err)
		return
	}
	httpjson.Write(w, http.StatusOK, map[string]bool{"ok": true})
}

func writeError(w http.ResponseWriter, err error) {
	var requestErr requestError
	if errors.As(err, &requestErr) {
		status := http.StatusBadRequest
		switch string(requestErr) {
		case ErrorNotFound, ErrorTargetNotFound:
			status = http.StatusNotFound
		case ErrorNoCurrentAccount:
			status = http.StatusConflict
		case ErrorDetectorUnavailable:
			status = http.StatusServiceUnavailable
		case ErrorDetectorBusy:
			status = http.StatusConflict
		}
		httpjson.WriteError(w, status, requestErr.Error())
		return
	}
	httpjson.WriteError(w, http.StatusInternalServerError, ErrorUnknown)
}
