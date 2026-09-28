package handler

import (
	"net/http/httptest"
	"testing"

	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// /api/v1/paw/* 在分组校验通过前没有 API key；这类报错也要记下是哪个用户。
func TestFillOpsUserFromAuthSubjectUsesSignedInUser(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: 42})
	entry := &service.OpsInsertErrorLogInput{}

	fillOpsUserFromAuthSubject(c, entry)

	require.NotNil(t, entry.UserID)
	require.Equal(t, int64(42), *entry.UserID)
}

func TestFillOpsUserFromAuthSubjectKeepsTheKeysUser(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: 42})
	keyUser := int64(7)
	entry := &service.OpsInsertErrorLogInput{UserID: &keyUser}

	fillOpsUserFromAuthSubject(c, entry)

	require.Equal(t, int64(7), *entry.UserID)
}

func TestFillOpsUserFromAuthSubjectWithoutSessionLeavesItEmpty(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	entry := &service.OpsInsertErrorLogInput{}

	fillOpsUserFromAuthSubject(c, entry)

	require.Nil(t, entry.UserID)
}

func TestSetOpsRequestedModelRecordsTrimmedModel(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())

	SetOpsRequestedModel(c, "  gpt-5.5  ")
	require.Equal(t, "gpt-5.5", c.GetString(opsModelKey))

	SetOpsRequestedModel(c, "   ")
	require.Equal(t, "gpt-5.5", c.GetString(opsModelKey), "an empty model does not erase one already recorded")
}
