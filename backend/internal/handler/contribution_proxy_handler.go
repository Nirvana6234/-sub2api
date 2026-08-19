package handler

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	dbaccount "github.com/Wei-Shaw/sub2api/ent/account"
	dbproxy "github.com/Wei-Shaw/sub2api/ent/proxy"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/Wei-Shaw/sub2api/internal/util/urlvalidator"
	"github.com/gin-gonic/gin"
)

const maxContributionProxiesPerUser = 20

type ContributionProxyView struct {
	ID          int64     `json:"id"`
	Name        string    `json:"name"`
	Protocol    string    `json:"protocol"`
	Host        string    `json:"host"`
	Port        int       `json:"port"`
	Username    string    `json:"username,omitempty"`
	HasPassword bool      `json:"has_password"`
	Status      string    `json:"status"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type CreateContributionProxyRequest struct {
	Name     string `json:"name"`
	Protocol string `json:"protocol"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Username string `json:"username"`
	Password string `json:"password"`
}

type UpdateContributionProxyRequest struct {
	Name     *string `json:"name"`
	Protocol *string `json:"protocol"`
	Host     *string `json:"host"`
	Port     *int    `json:"port"`
	Username *string `json:"username"`
	Password *string `json:"password"`
	Status   *string `json:"status"`
}

func (h *AccountContributionHandler) ListContributionProxies(c *gin.Context) {
	subject, user, ok := h.authenticatedUser(c)
	if !ok {
		return
	}
	if h.entClient == nil {
		response.Error(c, http.StatusServiceUnavailable, "Contribution proxy service unavailable")
		return
	}
	ownerPredicate := dbproxy.OwnerUserIDEQ(subject.UserID)
	if user.IsAdmin() {
		ownerPredicate = dbproxy.Or(ownerPredicate, dbproxy.OwnerUserIDIsNil())
	}
	items, err := h.entClient.Proxy.Query().
		Where(ownerPredicate).
		Order(dbent.Desc(dbproxy.FieldCreatedAt)).
		All(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	views := make([]ContributionProxyView, 0, len(items))
	for _, item := range items {
		views = append(views, contributionProxyView(item))
	}
	response.Success(c, views)
}

func (h *AccountContributionHandler) CreateContributionProxy(c *gin.Context) {
	subject, user, ok := h.authenticatedUser(c)
	if !ok {
		return
	}
	if h.entClient == nil {
		response.Error(c, http.StatusServiceUnavailable, "Contribution proxy service unavailable")
		return
	}
	var req CreateContributionProxyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	if err := normalizeContributionProxyCreate(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	if !user.IsAdmin() {
		if err := validateContributionProxyPublicHost(req.Host); err != nil {
			response.BadRequest(c, err.Error())
			return
		}
	}
	count, err := h.entClient.Proxy.Query().Where(dbproxy.OwnerUserIDEQ(subject.UserID)).Count(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	if count >= maxContributionProxiesPerUser {
		response.BadRequest(c, fmt.Sprintf("at most %d private proxies can be added", maxContributionProxiesPerUser))
		return
	}
	builder := h.entClient.Proxy.Create().
		SetName(req.Name).
		SetProtocol(req.Protocol).
		SetHost(req.Host).
		SetPort(req.Port).
		SetOwnerUserID(subject.UserID).
		SetStatus(service.StatusActive).
		SetFallbackMode(service.FallbackModeNone)
	if req.Username != "" {
		builder.SetUsername(req.Username)
	}
	if req.Password != "" {
		builder.SetPassword(req.Password)
	}
	created, err := builder.Save(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, contributionProxyView(created))
}

func (h *AccountContributionHandler) UpdateContributionProxy(c *gin.Context) {
	subject, user, ok := h.authenticatedUser(c)
	if !ok {
		return
	}
	proxyID, ok := contributionProxyID(c)
	if !ok {
		return
	}
	item, err := h.getOwnedContributionProxy(c.Request.Context(), subject.UserID, proxyID, false)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	var req UpdateContributionProxyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	name, protocol, host, port := item.Name, item.Protocol, item.Host, item.Port
	username := ""
	if item.Username != nil {
		username = *item.Username
	}
	if req.Name != nil {
		name = *req.Name
	}
	if req.Protocol != nil {
		protocol = *req.Protocol
	}
	if req.Host != nil {
		host = *req.Host
	}
	if req.Port != nil {
		port = *req.Port
	}
	if req.Username != nil {
		username = *req.Username
	}
	input := CreateContributionProxyRequest{Name: name, Protocol: protocol, Host: host, Port: port, Username: username}
	if err := normalizeContributionProxyCreate(&input); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	if req.Host != nil && !user.IsAdmin() {
		if err := validateContributionProxyPublicHost(input.Host); err != nil {
			response.BadRequest(c, err.Error())
			return
		}
	}
	builder := item.Update().
		SetName(input.Name).
		SetProtocol(input.Protocol).
		SetHost(input.Host).
		SetPort(input.Port)
	if input.Username == "" {
		builder.ClearUsername()
	} else {
		builder.SetUsername(input.Username)
	}
	if req.Password != nil {
		password := strings.TrimSpace(*req.Password)
		if len(password) > 100 {
			response.BadRequest(c, "password must be no more than 100 characters")
			return
		}
		if password == "" {
			builder.ClearPassword()
		} else {
			builder.SetPassword(password)
		}
	}
	if req.Status != nil {
		status := strings.ToLower(strings.TrimSpace(*req.Status))
		if status != service.StatusActive && status != "inactive" {
			response.BadRequest(c, "status must be active or inactive")
			return
		}
		builder.SetStatus(status)
	}
	updated, err := builder.Save(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, contributionProxyView(updated))
}

func (h *AccountContributionHandler) DeleteContributionProxy(c *gin.Context) {
	subject, _, ok := h.authenticatedUser(c)
	if !ok {
		return
	}
	proxyID, ok := contributionProxyID(c)
	if !ok {
		return
	}
	item, err := h.getOwnedContributionProxy(c.Request.Context(), subject.UserID, proxyID, false)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	inUse, err := h.entClient.Account.Query().Where(dbaccount.ProxyIDEQ(proxyID)).Exist(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	if inUse {
		response.Error(c, http.StatusConflict, "Switch accounts using this proxy to another connection before deleting it")
		return
	}
	if err := h.entClient.Proxy.DeleteOne(item).Exec(c.Request.Context()); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"message": "Proxy deleted successfully"})
}

func (h *AccountContributionHandler) TestContributionProxy(c *gin.Context) {
	subject, user, ok := h.authenticatedUser(c)
	if !ok {
		return
	}
	proxyID, ok := contributionProxyID(c)
	if !ok {
		return
	}
	item, err := h.getOwnedContributionProxy(c.Request.Context(), subject.UserID, proxyID, false)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	if h.proxyProber == nil {
		response.Error(c, http.StatusServiceUnavailable, "Proxy test service unavailable")
		return
	}
	if !user.IsAdmin() {
		if err := validateContributionProxyPublicHost(item.Host); err != nil {
			response.BadRequest(c, err.Error())
			return
		}
	}
	proxyValue := contributionProxyServiceValue(item)
	exitInfo, latencyMs, probeErr := h.proxyProber.ProbeProxy(c.Request.Context(), proxyValue.URL())
	if probeErr != nil {
		response.Success(c, service.ProxyTestResult{Success: false, Message: probeErr.Error()})
		return
	}
	response.Success(c, service.ProxyTestResult{
		Success: true, Message: "Proxy is accessible", LatencyMs: latencyMs,
		IPAddress: exitInfo.IP, City: exitInfo.City, Region: exitInfo.Region,
		Country: exitInfo.Country, CountryCode: exitInfo.CountryCode,
	})
}

func (h *AccountContributionHandler) getOwnedContributionProxy(ctx context.Context, userID, proxyID int64, requireActive bool) (*dbent.Proxy, error) {
	if h == nil || h.entClient == nil {
		return nil, fmt.Errorf("contribution proxy service unavailable")
	}
	user, err := h.userService.GetByID(ctx, userID)
	if err != nil {
		return nil, err
	}
	ownerPredicate := dbproxy.OwnerUserIDEQ(userID)
	if user.IsAdmin() {
		ownerPredicate = dbproxy.Or(ownerPredicate, dbproxy.OwnerUserIDIsNil())
	}
	item, err := h.entClient.Proxy.Query().
		Where(dbproxy.IDEQ(proxyID), ownerPredicate).
		Only(ctx)
	if err != nil {
		if dbent.IsNotFound(err) {
			return nil, service.ErrProxyNotFound
		}
		return nil, err
	}
	if requireActive && item.Status != service.StatusActive {
		return nil, fmt.Errorf("selected proxy is not active")
	}
	if requireActive && !user.IsAdmin() {
		if err := validateContributionProxyPublicHost(item.Host); err != nil {
			return nil, err
		}
	}
	return item, nil
}

func contributionProxyID(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("proxy_id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "invalid proxy id")
		return 0, false
	}
	return id, true
}

func normalizeContributionProxyCreate(req *CreateContributionProxyRequest) error {
	req.Name = strings.TrimSpace(req.Name)
	req.Protocol = strings.ToLower(strings.TrimSpace(req.Protocol))
	req.Host = strings.TrimSpace(req.Host)
	req.Username = strings.TrimSpace(req.Username)
	req.Password = strings.TrimSpace(req.Password)
	if req.Name == "" || len([]rune(req.Name)) > 100 {
		return fmt.Errorf("name is required and must be no more than 100 characters")
	}
	switch req.Protocol {
	case "http", "https", "socks5", "socks5h":
	default:
		return fmt.Errorf("protocol must be http, https, socks5, or socks5h")
	}
	if req.Host == "" || len(req.Host) > 255 || strings.Contains(req.Host, "://") || strings.ContainsAny(req.Host, "/?#@") || strings.ContainsFunc(req.Host, unicode.IsSpace) {
		return fmt.Errorf("host must be a valid proxy hostname or IP address without a scheme")
	}
	if req.Port < 1 || req.Port > 65535 {
		return fmt.Errorf("port must be between 1 and 65535")
	}
	if len([]rune(req.Username)) > 100 || len([]rune(req.Password)) > 100 {
		return fmt.Errorf("username and password must be no more than 100 characters")
	}
	return nil
}

func validateContributionProxyPublicHost(host string) error {
	if err := urlvalidator.ValidateResolvedIP(strings.TrimSpace(host)); err != nil {
		return fmt.Errorf("proxy host must resolve to a public IP address: %w", err)
	}
	return nil
}

func contributionProxyView(item *dbent.Proxy) ContributionProxyView {
	view := ContributionProxyView{
		ID: item.ID, Name: item.Name, Protocol: item.Protocol, Host: item.Host, Port: item.Port,
		HasPassword: item.Password != nil && *item.Password != "", Status: item.Status,
		CreatedAt: item.CreatedAt, UpdatedAt: item.UpdatedAt,
	}
	if item.Username != nil {
		view.Username = *item.Username
	}
	return view
}

func contributionProxyServiceValue(item *dbent.Proxy) *service.Proxy {
	proxyValue := &service.Proxy{ID: item.ID, Name: item.Name, Protocol: item.Protocol, Host: item.Host, Port: item.Port, Status: item.Status, OwnerUserID: item.OwnerUserID}
	if item.Username != nil {
		proxyValue.Username = *item.Username
	}
	if item.Password != nil {
		proxyValue.Password = *item.Password
	}
	return proxyValue
}
