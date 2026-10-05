package nodegw

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ip"
	"github.com/Wei-Shaw/sub2api/internal/pkg/requestmodel"
	"github.com/Wei-Shaw/sub2api/internal/relay/keycodec"
	"github.com/Wei-Shaw/sub2api/internal/relay/proto/relayv1"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
)

// AdmitMiddleware 是从节点上的准入中间件（设计 3.2）。它代替本地网关链上的全局 IP 黑名单、Key 鉴权与计费检查、
// 全局用户黑名单、自动分组路由、未分组拦截：Key 按本地同一规则取出，交主节点按本地中间件链复查；
// 拒绝原样写出，"暂不支持"交给主节点转发，通过时按 Key 快照设置鉴权上下文（与本地鉴权成功时相同）。
// 之后的分组模型白名单等中间件照常在本地跑。
//
// 请求体在这里读出并放回（交给主节点转发要用原始请求体）。
func (d *Dispatcher) AdmitMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		// TODO(WP11)：无效鉴权防刷计数（本地在 Redis）还没接到从节点。
		google := middleware2.IsGoogleRelayPath(c.Request.URL.Path)
		extract := middleware2.ExtractAPIKeyCredential
		if google {
			// Gemini 原生入口按本地 Google 鉴权的规则取 Key、写错误（x-goog-api-key 优先，/v1beta 的查询参数 key 可用）。
			extract = middleware2.ExtractGoogleAPIKeyCredential
		}
		rawKey, ok := extract(c, nil)
		if !ok {
			return
		}
		resp, err := d.deps.Select.Admit(c.Request.Context(), &relayv1.AdmitRequest{
			Credential: &relayv1.AdmitRequest_ApiKey{ApiKey: rawKey},
			ClientIp:   strings.TrimSpace(ip.GetClientIP(c)),
			Method:     c.Request.Method,
			Path:       c.Request.URL.Path,
		})
		if err != nil {
			slog.Warn("relay admit failed", "error", err)
			middleware2.MarkIngressRejected(c, middleware2.IngressRejectAPIKeyAuthOverloaded)
			if google {
				middleware2.GoogleErrorWriter(c, http.StatusServiceUnavailable, "API key authentication is temporarily unavailable")
				c.Abort()
				return
			}
			middleware2.AbortWithError(c, http.StatusServiceUnavailable, "API_KEY_AUTH_OVERLOADED", "API key authentication is temporarily unavailable")
			return
		}
		if r := resp.GetRejection(); r != nil && r.GetFormat() == relayv1.RejectionFormat_REJECTION_FORMAT_RAW {
			middleware2.WriteCapturedRejection(c, capturedRejection(r))
			return
		}

		body, err := httputil.ReadRequestBodyWithPrealloc(c.Request)
		// replay：Gemini 原生入口本地的链路里只有自动分组中间件会读请求体（按 URL 模型的白名单、组合平台选目标都不读），
		// 读失败（超限、断开）由处理函数自己读时报错（Google 格式）。非自动分组的 Key 把读到的错误原样交给处理函数。
		var replay error
		if err != nil {
			if !google || resp.GetAdmission() == nil {
				writeBodyReadError(c, err)
				return
			}
			replay = err
		}
		if replay == nil {
			requestmodel.ResetRequestBody(c.Request, body)
			stateOf(c).rawBody = body
		}

		if resp.GetRejection() != nil {
			// 暂不支持（未分组、非 OpenAI 分组等）：交给主节点转发。
			d.HandOff(c)
			c.Abort()
			return
		}
		adm := resp.GetAdmission()
		apiKey, err := keycodec.DecodeAPIKey(adm.GetApiKey(), rawKey)
		if err == nil {
			sub, subErr := keycodec.DecodeSubscription(adm.GetSubscription())
			if subErr == nil {
				if replay != nil {
					if apiKey.AutoGroup {
						writeBodyReadError(c, replay)
						return
					}
					c.Request.Body = erroringBody{err: replay}
				}
				middleware2.ReplaceAuthenticatedAPIKey(c, apiKey, sub)
				c.Next()
				return
			}
			err = subErr
		}
		slog.Error("relay admit: bad api key snapshot", "error", err)
		middleware2.AbortWithError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to validate API key")
	}
}

// writeBodyReadError 与分组模型白名单、自动分组中间件读请求体失败时的写法一致。
func writeBodyReadError(c *gin.Context, err error) {
	status, message := http.StatusBadRequest, "Failed to read request body"
	var maxErr *http.MaxBytesError
	if errors.As(err, &maxErr) {
		status, message = http.StatusRequestEntityTooLarge, "Request body is too large"
	}
	c.AbortWithStatusJSON(status, gin.H{"error": gin.H{"type": "invalid_request_error", "message": message}})
}

// erroringBody 把准入时读请求体遇到的错误原样交给后面读它的处理函数。
type erroringBody struct{ err error }

func (b erroringBody) Read([]byte) (int, error) { return 0, b.err }
func (erroringBody) Close() error               { return nil }

// readBody 读出请求体并放回。
func readBody(c *gin.Context) ([]byte, error) {
	body, err := httputil.ReadRequestBodyWithPrealloc(c.Request)
	if err != nil {
		return nil, err
	}
	requestmodel.ResetRequestBody(c.Request, body)
	return body, nil
}
