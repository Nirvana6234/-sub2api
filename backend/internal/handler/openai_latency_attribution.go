package handler

import (
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

// openAIServingGroupIDForLatency 返回本次请求实际被调度到的分组 ID。
//
// 延迟样本必须按「真实服务组」归属：走过兜底链路时是兜底组，否则是 API Key 的分组。
// 若按账号的组成员关系扇出，一次请求的耗时会被记到没有参与本次调度的组头上，
// 使得共用账号的两个分组读数强相关，兜底比较永远判定「目标不够快」。
func openAIServingGroupIDForLatency(c *gin.Context) int64 {
	if c == nil {
		return 0
	}
	requested := int64(0)
	if value, exists := c.Get("api_key"); exists {
		if apiKey, ok := value.(*service.APIKey); ok && apiKey != nil && apiKey.GroupID != nil {
			requested = *apiKey.GroupID
		}
	}
	return service.OpenAIServingGroupID(c.Request.Context(), requested)
}

// openAIReasoningEffortForLatency 取转发结果里的推理强度，用于延迟样本分桶。
// 为空表示未提供或不适用，归入普通档。
func openAIReasoningEffortForLatency(result *service.OpenAIForwardResult) string {
	if result == nil || result.ReasoningEffort == nil {
		return ""
	}
	return *result.ReasoningEffort
}
