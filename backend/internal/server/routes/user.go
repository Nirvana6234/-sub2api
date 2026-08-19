package routes

import (
	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

// RegisterUserRoutes 注册用户相关路由（需要认证）
func RegisterUserRoutes(
	v1 *gin.RouterGroup,
	h *handler.Handlers,
	jwtAuth middleware.JWTAuthMiddleware,
	auditLog middleware.AuditLogMiddleware,
	settingService *service.SettingService,
	panelRateLimiter *middleware.PanelRateLimiter,
) {
	authenticated := v1.Group("")
	authenticated.Use(gin.HandlerFunc(jwtAuth))
	authenticated.Use(middleware.BackendModeUserGuard(settingService))
	// 面板全局按用户限流：防止单个账号高频刷接口打爆数据库
	authenticated.Use(panelRateLimiter.Global())
	// 用户管理面变更类操作入审计（含 TOTP 启用/禁用、step-up 验证、密码修改等安全事件）
	authenticated.Use(gin.HandlerFunc(auditLog))
	{
		// 用户接口
		user := authenticated.Group("/user")
		{
			user.GET("/profile", h.User.GetProfile)
			user.PUT("/password", h.User.ChangePassword)
			user.PUT("", h.User.UpdateProfile)
			user.GET("/aff", h.User.GetAffiliate)
			user.POST("/aff/transfer", h.User.TransferAffiliateQuota)
			user.POST("/account-bindings/email/send-code", h.User.SendEmailBindingCode)
			user.POST("/account-bindings/email", h.User.BindEmailIdentity)
			user.DELETE("/account-bindings/:provider", h.User.UnbindIdentity)
			user.POST("/auth-identities/bind/start", h.User.StartIdentityBinding)
			user.GET("/api-keys/:id/usage/daily", panelRateLimiter.Heavy(), h.Usage.GetMyAPIKeyDailyUsage)
			user.GET("/platform-quotas", h.User.GetMyPlatformQuotas)

			// 通知邮箱管理
			notifyEmail := user.Group("/notify-email")
			{
				notifyEmail.POST("/send-code", h.User.SendNotifyEmailCode)
				notifyEmail.POST("/verify", h.User.VerifyNotifyEmail)
				notifyEmail.PUT("/toggle", h.User.ToggleNotifyEmail)
				notifyEmail.DELETE("", h.User.RemoveNotifyEmail)
			}

			// TOTP 双因素认证
			totp := user.Group("/totp")
			{
				totp.GET("/status", h.Totp.GetStatus)
				totp.GET("/verification-method", h.Totp.GetVerificationMethod)
				totp.POST("/send-code", h.Totp.SendVerifyCode)
				totp.POST("/setup", h.Totp.InitiateSetup)
				totp.POST("/enable", h.Totp.Enable)
				totp.POST("/disable", h.Totp.Disable)
				// 敏感操作二次验证：授予当前会话一段时间的 step-up 权限
				totp.POST("/step-up", h.Totp.StepUp)
			}

			passkeys := user.Group("/passkeys")
			{
				passkeys.GET("", h.Passkey.List)
				passkeys.POST("/register/begin", h.Passkey.BeginRegistration)
				passkeys.POST("/register/finish", h.Passkey.FinishRegistration)
				passkeys.PATCH("/:id", h.Passkey.Rename)
				passkeys.DELETE("/:id", h.Passkey.Delete)
			}
		}

		// API Key管理
		keys := authenticated.Group("/keys")
		{
			keys.GET("", h.APIKey.List)
			keys.POST("/playground/ensure", h.APIKey.EnsurePlayground)
			keys.GET("/:id", h.APIKey.GetByID)
			keys.POST("", h.APIKey.Create)
			keys.PUT("/:id", h.APIKey.Update)
			keys.DELETE("/:id", h.APIKey.Delete)
		}

		// 用户可用分组（非管理员接口）
		groups := authenticated.Group("/groups")
		{
			groups.GET("/available", h.APIKey.GetAvailableGroups)
			groups.GET("/rates", h.APIKey.GetUserGroupRates)
		}

		// 用户可用渠道（非管理员接口）
		channels := authenticated.Group("/channels")
		{
			channels.GET("/available", h.AvailableChannel.List)
		}

		// 用户贡献账号默认仅本人使用；通过贡献房间明确选择后才可共享。
		contributions := authenticated.Group("/account-contributions")
		contributions.Use(middleware.RequireUserFeature(middleware.UserFeatureAccountManagement))
		{
			contributions.GET("", h.AccountContribution.List)
			contributions.GET("/groups", h.AccountContribution.ListGroups)
			contributions.GET("/pool-groups", h.AccountContribution.ListPoolGroups)
			contributions.GET("/proxies", h.AccountContribution.ListContributionProxies)
			contributions.POST("/proxies", h.AccountContribution.CreateContributionProxy)
			contributions.PUT("/proxies/:proxy_id", h.AccountContribution.UpdateContributionProxy)
			contributions.DELETE("/proxies/:proxy_id", h.AccountContribution.DeleteContributionProxy)
			contributions.POST("/proxies/:proxy_id/test", h.AccountContribution.TestContributionProxy)
			contributions.POST("", h.AccountContribution.Submit)
			contributions.POST("/openai/generate-auth-url", h.AccountContribution.GenerateOpenAIContributionAuthURL)
			contributions.POST("/openai/create-from-code", h.AccountContribution.CreateOpenAIContributionFromCode)
			contributions.POST("/openai/create-from-refresh-token", h.AccountContribution.CreateOpenAIContributionFromRefreshToken)
			contributions.POST("/openai/create-from-mobile-refresh-token", h.AccountContribution.CreateOpenAIContributionFromMobileRefreshToken)
			contributions.POST("/openai/create-from-codex-pat", h.AccountContribution.CreateOpenAIContributionFromCodexPAT)
			contributions.POST("/anthropic/generate-auth-url", h.AccountContribution.GenerateAnthropicContributionAuthURL)
			contributions.POST("/anthropic/create-from-code", h.AccountContribution.CreateAnthropicContributionFromCode)
			contributions.GET("/room", h.AccountContribution.GetOwnRoom)
			contributions.POST("/room", h.AccountContribution.CreateOwnRoom)
			contributions.PUT("/room", h.AccountContribution.UpdateOwnRoom)
			contributions.POST("/room/accounts", h.AccountContribution.AddOwnRoomAccount)
			contributions.PATCH("/room/accounts/:account_id", h.AccountContribution.UpdateOwnRoomAccount)
			contributions.DELETE("/room/accounts/:account_id", h.AccountContribution.DeleteOwnRoomAccount)
			contributions.GET("/:id/usage-summary", h.AccountContribution.GetUsageSummary)
			contributions.GET("/:id/models", h.AccountContribution.GetAvailableModels)
			contributions.PUT("/:id", h.AccountContribution.Update)
			contributions.DELETE("/:id", h.AccountContribution.Delete)
			contributions.POST("/:id/test", h.AccountContribution.Test)
			contributions.POST("/:id/test-stream", h.AccountContribution.TestStream)
		}

		// Room selection is independent from group selection and is persisted per
		// user. Public-pool fallback is always an explicit opt-in.
		contributionRooms := authenticated.Group("/contribution-rooms")
		contributionRooms.Use(middleware.RequireUserFeature(middleware.UserFeatureContributionRooms))
		{
			contributionRooms.GET("", h.AccountContribution.ListSelectableContributionRooms)
			contributionRooms.GET("/preference", h.AccountContribution.GetContributionRoomPreference)
			contributionRooms.PUT("/preference", h.AccountContribution.UpdateContributionRoomPreference)
			contributionRooms.DELETE("/preference", h.AccountContribution.DeleteContributionRoomPreference)
		}

		// 使用记录（聚合统计属重查询，叠加更严格的按用户限流）
		usage := authenticated.Group("/usage")
		usage.Use(panelRateLimiter.Heavy())
		{
			usage.GET("", h.Usage.List)
			usage.GET("/errors", h.Usage.ListErrors)
			usage.GET("/errors/:id", h.Usage.GetErrorDetail)
			usage.GET("/:id", h.Usage.GetByID)
			usage.GET("/stats", h.Usage.Stats)
			// User dashboard endpoints
			usage.GET("/dashboard/stats", h.Usage.DashboardStats)
			usage.GET("/dashboard/trend", h.Usage.DashboardTrend)
			usage.GET("/dashboard/models", h.Usage.DashboardModels)
			usage.GET("/dashboard/snapshot-v2", h.Usage.DashboardSnapshotV2)
			usage.POST("/dashboard/api-keys-usage", h.Usage.DashboardAPIKeysUsage)
		}

		// 公告（用户可见）
		announcements := authenticated.Group("/announcements")
		{
			announcements.GET("", h.Announcement.List)
			announcements.POST("/:id/read", h.Announcement.MarkRead)
		}

		// 卡密兑换
		redeem := authenticated.Group("/redeem")
		{
			redeem.POST("", h.Redeem.Redeem)
			redeem.GET("/history", h.Redeem.GetHistory)
		}

		// 用户订阅
		subscriptions := authenticated.Group("/subscriptions")
		{
			subscriptions.GET("", h.Subscription.List)
			subscriptions.GET("/active", h.Subscription.GetActive)
			subscriptions.GET("/progress", h.Subscription.GetProgress)
			subscriptions.GET("/summary", h.Subscription.GetSummary)
		}

		// 渠道监控（用户只读）
		monitors := authenticated.Group("/channel-monitors")
		{
			monitors.GET("", h.ChannelMonitor.List)
			monitors.GET("/:id/status", h.ChannelMonitor.GetStatus)
		}

		// V2 passive views require feature on + mode=v2.
		monitorV2 := authenticated.Group("/channel-monitor-v2")
		monitorV2.Use(panelRateLimiter.Heavy())
		monitorV2.Use(channelMonitorModeV2Guard(settingService))
		{
			monitorV2.GET("/dimensions", h.ChannelMonitorV2.Dimensions)
			monitorV2.GET("/snapshot", h.ChannelMonitorV2.Snapshot)
			monitorV2.GET("/models", h.ChannelMonitorV2.Models)
			monitorV2.GET("/matrix", h.ChannelMonitorV2.Matrix)
			monitorV2.GET("/errors", h.ChannelMonitorV2.Errors)
			monitorV2.GET("/users", h.ChannelMonitorV2.Users)
		}
	}
}
