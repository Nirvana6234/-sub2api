package middleware

import (
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// RechargeBlockedGuard 拦截被列入充值黑名单的用户对 /payment 下所有接口的访问。
//
// 名单来自 settings.recharge_blocked_user_ids（JSON 数组），改名单不需要发版。
//
// 必须在后端拦截而不能只靠前端隐藏入口：前端隐藏只是视觉效果，用户拿到 JWT 后
// 直接调 POST /payment/orders 仍然能下单。这里挂在 /payment 路由组上，配置、
// 套餐、下单、验单全部一并挡住。
//
// 与 BackendModeUserGuard 的差别：那个按全局模式挡所有非管理员，这个按用户名单
// 挡指定的人，因此**不对管理员放行**——管理员若被列入名单同样受限，避免出现
// "给管理员充值绕过限制" 的口子。
func RechargeBlockedGuard(settingService *service.SettingService) gin.HandlerFunc {
	return func(c *gin.Context) {
		if settingService == nil {
			c.Next()
			return
		}
		subject, ok := GetAuthSubjectFromContext(c)
		if !ok || subject.UserID <= 0 {
			c.Next()
			return
		}
		if !settingService.IsRechargeBlockedUser(c.Request.Context(), subject.UserID) {
			c.Next()
			return
		}
		response.Forbidden(c, "Recharge is disabled for this account.")
		c.Abort()
	}
}
