package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"

	dbproxy "github.com/Wei-Shaw/sub2api/ent/proxy"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type contributionProxyProberStub struct {
	url string
}

func (s *contributionProxyProberStub) ProbeProxy(_ context.Context, proxyURL string) (*service.ProxyExitInfo, int64, error) {
	s.url = proxyURL
	return &service.ProxyExitInfo{IP: "203.0.113.8", Country: "Test"}, 42, nil
}

func TestContributionProxyCreateIsPrivateAndDoesNotReturnPassword(t *testing.T) {
	client := newContributionRoomTestClient(t)
	owner := createContributionRoomTestUser(t, client, "proxy-owner@example.com")
	h := newContributionRoomTestHandler(owner.ID, nil, client)
	c, recorder := contributionRoomTestContext(http.MethodPost, "/account-contributions/proxies", owner.ID,
		`{"name":"Home proxy","protocol":"socks5","host":"203.0.113.8","port":1080,"username":"alice","password":"secret"}`)

	h.CreateContributionProxy(c)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.NotContains(t, recorder.Body.String(), "secret")
	stored := client.Proxy.Query().Where(dbproxy.OwnerUserIDEQ(owner.ID)).OnlyX(context.Background())
	require.NotNil(t, stored.Password)
	require.Equal(t, "secret", *stored.Password)

	other := createContributionRoomTestUser(t, client, "proxy-other@example.com")
	otherHandler := newContributionRoomTestHandler(other.ID, nil, client)
	c, recorder = contributionRoomTestContext(http.MethodGet, "/account-contributions/proxies", other.ID, "")
	otherHandler.ListContributionProxies(c)
	require.Equal(t, http.StatusOK, recorder.Code)
	var envelope struct {
		Data []ContributionProxyView `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &envelope))
	require.Empty(t, envelope.Data)
}

func TestContributionProxyRejectsPrivateNetworkHost(t *testing.T) {
	client := newContributionRoomTestClient(t)
	owner := createContributionRoomTestUser(t, client, "proxy-private-host@example.com")
	h := newContributionRoomTestHandler(owner.ID, nil, client)
	c, recorder := contributionRoomTestContext(http.MethodPost, "/account-contributions/proxies", owner.ID,
		`{"name":"Local proxy","protocol":"http","host":"127.0.0.1","port":8080}`)

	h.CreateContributionProxy(c)

	require.Equal(t, http.StatusBadRequest, recorder.Code)
	require.Contains(t, recorder.Body.String(), "public IP address")
	require.False(t, client.Proxy.Query().Where(dbproxy.OwnerUserIDEQ(owner.ID)).ExistX(context.Background()))
}

func TestContributionProxyAdminCanUseGlobalAndLoopbackProxies(t *testing.T) {
	client := newContributionRoomTestClient(t)
	admin := client.User.Create().
		SetEmail("proxy-admin@example.com").
		SetPasswordHash("test-hash").
		SetUsername("proxy-admin@example.com").
		SetRole(service.RoleAdmin).
		SaveX(context.Background())
	global := client.Proxy.Create().
		SetName("Local v2rayN").
		SetProtocol("http").
		SetHost("127.0.0.1").
		SetPort(10808).
		SaveX(context.Background())
	h := newContributionRoomTestHandler(admin.ID, nil, client)
	h.userService = service.NewUserService(&contributionUserRepoStub{user: &service.User{
		ID: admin.ID, Email: admin.Email, Role: service.RoleAdmin,
	}}, nil, nil, nil)

	c, recorder := contributionRoomTestContext(http.MethodGet, "/account-contributions/proxies", admin.ID, "")
	h.ListContributionProxies(c)
	require.Equal(t, http.StatusOK, recorder.Code)
	require.Contains(t, recorder.Body.String(), "Local v2rayN")

	c, recorder = contributionRoomTestContext(http.MethodPost, "/account-contributions/proxies", admin.ID,
		`{"name":"Second local proxy","protocol":"http","host":"127.0.0.1","port":7890}`)
	h.CreateContributionProxy(c)
	require.Equal(t, http.StatusOK, recorder.Code)
	require.True(t, client.Proxy.Query().Where(dbproxy.OwnerUserIDEQ(admin.ID)).ExistX(context.Background()))

	selected, err := h.getOwnedContributionProxy(context.Background(), admin.ID, global.ID, true)
	require.NoError(t, err)
	require.Equal(t, global.ID, selected.ID)
}

func TestContributionProxyCannotBeManagedByAnotherUser(t *testing.T) {
	client := newContributionRoomTestClient(t)
	owner := createContributionRoomTestUser(t, client, "proxy-owner-2@example.com")
	other := createContributionRoomTestUser(t, client, "proxy-other-2@example.com")
	item := client.Proxy.Create().
		SetName("Private proxy").
		SetProtocol("http").
		SetHost("203.0.113.9").
		SetPort(8080).
		SetOwnerUserID(owner.ID).
		SaveX(context.Background())
	h := newContributionRoomTestHandler(other.ID, nil, client)
	c, recorder := contributionRoomTestContext(http.MethodPut, "/account-contributions/proxies/1", other.ID, `{"name":"stolen"}`)
	c.Params = gin.Params{{Key: "proxy_id", Value: strconv.FormatInt(item.ID, 10)}}

	h.UpdateContributionProxy(c)

	require.Equal(t, http.StatusNotFound, recorder.Code)
	require.Equal(t, "Private proxy", client.Proxy.GetX(context.Background(), item.ID).Name)
}

func TestContributionProxyTestUsesStoredCredentialsWithoutReturningThem(t *testing.T) {
	client := newContributionRoomTestClient(t)
	owner := createContributionRoomTestUser(t, client, "proxy-owner-3@example.com")
	item := client.Proxy.Create().
		SetName("Private proxy").
		SetProtocol("http").
		SetHost("203.0.113.10").
		SetPort(8080).
		SetUsername("alice").
		SetPassword("secret").
		SetOwnerUserID(owner.ID).
		SaveX(context.Background())
	prober := &contributionProxyProberStub{}
	h := newContributionRoomTestHandler(owner.ID, nil, client)
	h.proxyProber = prober
	c, recorder := contributionRoomTestContext(http.MethodPost, "/account-contributions/proxies/1/test", owner.ID, "")
	c.Params = gin.Params{{Key: "proxy_id", Value: strconv.FormatInt(item.ID, 10)}}

	h.TestContributionProxy(c)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.True(t, strings.Contains(prober.url, "alice:secret@"))
	require.NotContains(t, recorder.Body.String(), "secret")
	require.Contains(t, recorder.Body.String(), "203.0.113.8")
}
