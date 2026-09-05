package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type playgroundHistoryHandlerRepoStub struct {
	state  json.RawMessage
	userID int64
}

func (r *playgroundHistoryHandlerRepoStub) Get(_ context.Context, userID int64) (json.RawMessage, error) {
	if userID != r.userID {
		return nil, nil
	}
	return r.state, nil
}

func (r *playgroundHistoryHandlerRepoStub) Save(_ context.Context, userID int64, state json.RawMessage, _ time.Time) error {
	r.userID = userID
	r.state = append(json.RawMessage(nil), state...)
	return nil
}

func playgroundHistoryHandlerPayload() string {
	return `{"version":2,"model":"gpt-4.1","parameters":{"stream":true},"activeConversationId":null,"projects":[],"conversations":[]}`
}

func newPlaygroundHistoryHandlerContext(method, target, body string) (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(method, target, strings.NewReader(body))
	context, _ := gin.CreateTestContext(recorder)
	context.Request = request
	context.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 7})
	return context, recorder
}

func TestPlaygroundHistoryHandlerSavesAndReadsUserState(t *testing.T) {
	repo := &playgroundHistoryHandlerRepoStub{}
	h := NewPlaygroundHistoryHandler(service.NewPlaygroundHistoryService(repo))

	saveContext, saveRecorder := newPlaygroundHistoryHandlerContext(http.MethodPut, "/api/v1/playground/history", playgroundHistoryHandlerPayload())
	h.Save(saveContext)
	require.Equal(t, http.StatusOK, saveRecorder.Code)
	require.Equal(t, int64(7), repo.userID)

	getContext, getRecorder := newPlaygroundHistoryHandlerContext(http.MethodGet, "/api/v1/playground/history", "")
	h.Get(getContext)
	require.Equal(t, http.StatusOK, getRecorder.Code)
	require.Contains(t, getRecorder.Body.String(), `"version":2`)
	require.Contains(t, getRecorder.Body.String(), `"model":"gpt-4.1"`)
}

func TestPlaygroundHistoryHandlerDoesNotRequireAPIKeyContext(t *testing.T) {
	repo := &playgroundHistoryHandlerRepoStub{}
	h := NewPlaygroundHistoryHandler(service.NewPlaygroundHistoryService(repo))
	context, recorder := newPlaygroundHistoryHandlerContext(http.MethodPut, "/api/v1/playground/history", playgroundHistoryHandlerPayload())
	h.Save(context)
	require.Equal(t, http.StatusOK, recorder.Code)
	require.NotEmpty(t, repo.state)
}
