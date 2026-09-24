package middleware

import (
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestAuthenticatedWorkspaceClientKeyMarksLocalFallbackRoute(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest("GET", "/v1/models", nil)
	apiKey := &service.APIKey{
		ID:   10,
		Name: "共飞工作台-Codex-客户端",
		User: &service.User{ID: 20},
	}

	setAuthenticatedAPIKeyRequestContext(ctx, apiKey)

	require.Equal(t, int64(20), ctx.Request.Context().Value(ctxkey.UserID))
	require.Equal(t, int64(10), ctx.Request.Context().Value(ctxkey.APIKeyID))
	require.Equal(t, true, ctx.Request.Context().Value(ctxkey.WorkspaceLocalFallbackRoute))
}

func TestOrdinaryClientKeyDoesNotMarkLocalFallbackRoute(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest("GET", "/v1/models", nil)

	setAuthenticatedAPIKeyRequestContext(ctx, &service.APIKey{
		ID:   10,
		Name: "ordinary-client",
		User: &service.User{ID: 20},
	})

	require.Nil(t, ctx.Request.Context().Value(ctxkey.WorkspaceLocalFallbackRoute))
}

func TestAuthenticatedWorkspaceClientKeyWithoutHydratedUserMarksLocalFallbackRoute(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest("GET", "/v1/models", nil)
	apiKey := &service.APIKey{
		ID:     10,
		UserID: 20,
		Name:   "共飞工作台-Codex-客户端",
	}

	setAuthenticatedAPIKeyRequestContext(ctx, apiKey)

	require.Equal(t, int64(20), ctx.Request.Context().Value(ctxkey.UserID))
	require.Equal(t, int64(10), ctx.Request.Context().Value(ctxkey.APIKeyID))
	require.Equal(t, true, ctx.Request.Context().Value(ctxkey.WorkspaceLocalFallbackRoute))
}
