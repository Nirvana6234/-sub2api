package routes

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type pawResponsesGroupSource []service.Group

func (s pawResponsesGroupSource) AvailableGroups(context.Context, int64) ([]service.Group, error) {
	return append([]service.Group(nil), s...), nil
}

func TestPawResponsesGroupHeaderOverridesSavedDefaultsAndInternalRoutingState(t *testing.T) {
	gin.SetMode(gin.TestMode)
	groups := pawResponsesGroupSource{
		{ID: 7, Name: "A", Platform: service.PlatformOpenAI, Status: service.StatusActive},
		{ID: 8, Name: "B", Platform: service.PlatformOpenAI, Status: service.StatusActive},
	}
	defaults := &pawRouteStore{defaults: service.PawDefaults{GroupID: 7, ModelID: "gpt-5"}}
	config := service.NewPawConfigService(groups, pawChatRouteUsers{}, pawChatRouteChannels{}, defaults)
	oldGroupID := int64(7)
	internalKey := &service.APIKey{
		ID: 99, UserID: 42, Status: service.StatusActive,
		User:    &service.User{ID: 42, Status: service.StatusActive},
		GroupID: &oldGroupID, Group: &groups[0], AutoGroup: true,
		AutoGroupIDs: []int64{7, 8}, AutoGroupCurrentGroup: &groups[0],
		AutoGroupCurrentModel: "gpt-5",
	}
	chat := service.NewPawChatService(config, &pawChatRouteKeySource{apiKey: internalKey})
	var observed *service.APIKey
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 42})
		c.Next()
		observed, _ = middleware.GetAPIKeyFromContext(c)
	})
	r.POST("/api/v1/paw/responses", pawResponsesHandler(chat, PawRouteDependencies{}))

	for _, tc := range []struct {
		name, header string
		wantGroup    int64
		wantAuto     bool
		wantStatus   int
		wantError    string
	}{
		{"initial group", "7", 7, false, http.StatusServiceUnavailable, PawErrorCodeUpstreamUnavailable},
		{"switch while default stays A", "8", 8, false, http.StatusServiceUnavailable, PawErrorCodeUpstreamUnavailable},
		{"switch back", "7", 7, false, http.StatusServiceUnavailable, PawErrorCodeUpstreamUnavailable},
		{"missing header selects automatically", "", 7, true, http.StatusServiceUnavailable, PawErrorCodeUpstreamUnavailable},
		{"zero selects automatically", "0", 7, true, http.StatusServiceUnavailable, PawErrorCodeUpstreamUnavailable},
		{"auto selects automatically", "auto", 7, true, http.StatusServiceUnavailable, PawErrorCodeUpstreamUnavailable},
		{"invalid header", "invalid", 0, false, http.StatusBadRequest, PawErrorCodeGroupForbidden},
		{"negative header", "-1", 0, false, http.StatusBadRequest, PawErrorCodeGroupForbidden},
		{"unavailable group", "999", 0, false, http.StatusForbidden, PawErrorCodeGroupForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			observed = nil
			req := httptest.NewRequest(http.MethodPost, "/api/v1/paw/responses", strings.NewReader(`{"model":"gpt-5","input":[],"stream":true}`))
			req.Header.Set(PawGroupHeader, tc.header)
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			// No upstream is attached: valid requests reach the dispatch boundary.
			require.Equal(t, tc.wantStatus, w.Code, w.Body.String())
			require.Contains(t, w.Body.String(), tc.wantError)
			if tc.wantGroup == 0 {
				require.Nil(t, observed, "must not fall back to a default group")
				return
			}
			require.NotNil(t, observed)
			require.NotSame(t, internalKey, observed)
			require.NotNil(t, observed.GroupID)
			require.Equal(t, tc.wantGroup, *observed.GroupID)
			require.Equal(t, tc.wantGroup, observed.Group.ID)
			require.Equal(t, tc.wantAuto, observed.AutoGroup)
			if tc.wantAuto {
				require.Equal(t, []int64{7, 8}, observed.AutoGroupIDs)
				require.NotNil(t, observed.AutoGroupCurrentGroup)
				require.Equal(t, "gpt-5", observed.AutoGroupCurrentModel)
			} else {
				require.Empty(t, observed.AutoGroupIDs)
				require.Nil(t, observed.AutoGroupCurrentGroup)
				require.Empty(t, observed.AutoGroupCurrentModel)
			}
			require.Equal(t, int64(7), defaults.defaults.GroupID)
			require.Equal(t, int64(7), *internalKey.GroupID)
			require.True(t, internalKey.AutoGroup)
		})
	}
}
