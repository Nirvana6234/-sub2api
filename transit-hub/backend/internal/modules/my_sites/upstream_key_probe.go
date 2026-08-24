package my_sites

import (
	"context"
	"sort"
	"strings"

	"transithub/backend/internal/modules/upstream"
)

// modelSampleLimit 是回给前端的模型样例条数。上游动辄挂几百个模型，
// 全塞进响应里对判断「这个 key 能不能用」没有任何帮助。
const modelSampleLimit = 8

// cheapModelHints 是挑试探模型时的优先级关键字（从便宜到贵）。
//
// 测试会真实计费。虽然 max_tokens=1 的一次请求成本可以忽略，但没理由默认去打
// 一个 o1-pro：同一个分组里只要有 mini/flash/haiku 这类小模型，用它验证链路
// 通不通的结论完全一样，成本却低一到两个数量级。
var cheapModelHints = []string{"mini", "flash", "haiku", "lite", "small", "turbo", "3.5"}

// SetUpstreamKeyTester 注入探活能力。没注入时测试接口返回明确错误，
// 而不是让整个模块起不来——这个能力对既有功能是可选的。
func (s *Service) SetUpstreamKeyTester(tester UpstreamKeyTester) {
	s.keyTester = tester
}

// TestUpstreamCredential 测一个上游 Key 到底能不能用。
//
// 分两段：先 /v1/models 验 key 有效性并拿到模型池，再挑一个模型发一次
// max_tokens=1 的真实请求。只做第一段会漏掉「列表里有、实际请求回 503
// 无可用渠道」这类最常见的坑；只做第二段又分不清是 key 废了还是模型没挂上。
//
// 明文 key 只在这个函数的栈上出现：从上游列表里解析出来，发完请求就丢，
// 既不回给前端也不落库。
func (s *Service) TestUpstreamCredential(ctx context.Context, userID string, req UpstreamKeyTestRequest) (UpstreamKeyTestResponse, error) {
	var response UpstreamKeyTestResponse
	if s.keyTester == nil {
		return response, requestError(ErrorTesterUnavailable)
	}

	adminAccountID, err := s.currentAdminAccountID(ctx, userID)
	if err != nil {
		return response, err
	}
	// listOwnedUpstreamKeys 顺带做了站点归属校验，越权拿不到 key。
	site, keys, err := s.listOwnedUpstreamKeys(ctx, userID, adminAccountID, req.UpstreamSiteID)
	if err != nil {
		return response, err
	}

	keyID := strings.TrimSpace(req.UpstreamKeyID)
	if keyID == "" {
		keyID = s.connectedKeyID(ctx, userID, adminAccountID, req)
	}

	credential, ok := selectCredential(keys, keyID, req.UpstreamGroupID, req.UpstreamGroupName)
	if !ok {
		return response, requestError(ErrorCredentialNotFound)
	}

	response.KeyID = credential.ID
	response.KeyName = credential.Name
	response.KeyPreview = safeCredentialPreview(credential.Key)

	baseURL := strings.TrimSpace(site.BaseURL)
	models, err := s.keyTester.ListModels(ctx, baseURL, credential.Key)
	if err != nil {
		response.Models = UpstreamKeyTestStage{ErrorKey: ErrorModelListUnavailable}
		// 模型列表都拿不到就没有可测的模型，第二段如实标成跳过，
		// 不要显示成「对话测试失败」——那会把人引去查模型配置。
		response.Chat = UpstreamKeyTestStage{Skipped: true}
		return response, nil
	}

	response.Models = UpstreamKeyTestStage{OK: true}
	response.ModelCount = len(models)
	response.ModelSample = sampleModels(models)
	if len(models) == 0 {
		// key 有效但分组下一个模型都没挂：这是配置问题，不是 key 问题。
		response.Models = UpstreamKeyTestStage{ErrorKey: ErrorModelListEmpty}
		response.Chat = UpstreamKeyTestStage{Skipped: true}
		return response, nil
	}

	model := strings.TrimSpace(req.Model)
	if model == "" {
		model = pickProbeModel(models)
	}
	response.TestedModel = model

	outcome := s.keyTester.ProbeChat(ctx, baseURL, credential.Key, model)
	response.Chat = UpstreamKeyTestStage{
		OK:        outcome.OK,
		LatencyMs: outcome.LatencyMs,
		Detail:    outcome.Detail,
	}
	if !outcome.OK {
		response.Chat.ErrorKey = ErrorChatProbeFailed
		// 把探活的结果分类一并带上：auth_failed 和 model_not_found 要修的
		// 是完全不同的东西，只说一句「失败」等于什么都没说。
		if outcome.Result != "" {
			response.Chat.Detail = strings.TrimSpace(outcome.Result + " " + outcome.Detail)
		}
	}
	return response, nil
}

// ListUpstreamCredentialModels resolves a key with the same ownership checks
// as the test endpoint, then returns its complete model inventory without
// issuing a chat request.
func (s *Service) ListUpstreamCredentialModels(ctx context.Context, userID string, req UpstreamKeyTestRequest) (UpstreamKeyModelsResponse, error) {
	var response UpstreamKeyModelsResponse
	if s.keyTester == nil {
		return response, requestError(ErrorTesterUnavailable)
	}
	adminAccountID, err := s.currentAdminAccountID(ctx, userID)
	if err != nil {
		return response, err
	}
	site, keys, err := s.listOwnedUpstreamKeys(ctx, userID, adminAccountID, req.UpstreamSiteID)
	if err != nil {
		return response, err
	}
	keyID := strings.TrimSpace(req.UpstreamKeyID)
	if keyID == "" {
		keyID = s.connectedKeyID(ctx, userID, adminAccountID, req)
	}
	credential, ok := selectCredential(keys, keyID, req.UpstreamGroupID, req.UpstreamGroupName)
	if !ok {
		return response, requestError(ErrorCredentialNotFound)
	}
	response.KeyID = credential.ID
	response.KeyName = credential.Name
	response.KeyPreview = safeCredentialPreview(credential.Key)
	models, err := s.keyTester.ListModels(ctx, strings.TrimSpace(site.BaseURL), credential.Key)
	if err != nil {
		return response, requestError(ErrorModelListUnavailable)
	}
	response.Models = sampleAllModels(models)
	return response, nil
}

// connectedKeyID 找该站点+分组已对接连接记录里用的那个 Key。
// 查不到返回空串，调用方会退回「该分组下第一个凭据」。
func (s *Service) connectedKeyID(ctx context.Context, userID string, adminAccountID string, req UpstreamKeyTestRequest) string {
	if s.connRepository == nil {
		return ""
	}
	connections, err := s.connRepository.ListRealConnections(ctx, userID, adminAccountID)
	if err != nil {
		return ""
	}
	siteID := strings.TrimSpace(req.UpstreamSiteID)
	groupID := strings.TrimSpace(req.UpstreamGroupID)
	groupName := strings.TrimSpace(req.UpstreamGroupName)
	for _, connection := range connections {
		if strings.TrimSpace(connection.UpstreamSiteID) != siteID {
			continue
		}
		if !connectionMatchesGroup(connection, groupID, groupName) {
			continue
		}
		if keyID := strings.TrimSpace(connection.UpstreamKeyID); keyID != "" {
			return keyID
		}
	}
	return ""
}

func connectionMatchesGroup(connection RealConnection, groupID string, groupName string) bool {
	connGroupID := strings.TrimSpace(connection.UpstreamGroupID)
	connGroupName := strings.TrimSpace(connection.UpstreamGroupName)
	if connGroupID != "" && (connGroupID == groupID || connGroupID == groupName) {
		return true
	}
	return connGroupName != "" && (connGroupName == groupName || connGroupName == groupID)
}

// selectCredential 按 keyID 精确取；keyID 为空时退回该分组下第一个凭据。
func selectCredential(keys []upstream.Sub2APIKeyItem, keyID string, groupID string, groupName string) (upstream.Sub2APIKeyItem, bool) {
	if keyID != "" {
		for _, key := range keys {
			if strings.TrimSpace(key.ID) == keyID && strings.TrimSpace(key.Key) != "" {
				return key, true
			}
		}
	}
	for _, key := range keys {
		if strings.TrimSpace(key.Key) == "" {
			continue
		}
		if credentialMatchesGroup(key, groupID, groupName) {
			return key, true
		}
	}
	return upstream.Sub2APIKeyItem{}, false
}

// pickProbeModel 从模型池里挑一个最便宜的候选，挑不出就用排序后的第一个。
func pickProbeModel(models []string) string {
	normalized := make([]string, 0, len(models))
	for _, model := range models {
		if trimmed := strings.TrimSpace(model); trimmed != "" {
			normalized = append(normalized, trimmed)
		}
	}
	if len(normalized) == 0 {
		return ""
	}
	sort.Strings(normalized)
	for _, hint := range cheapModelHints {
		for _, model := range normalized {
			if strings.Contains(strings.ToLower(model), hint) {
				return model
			}
		}
	}
	return normalized[0]
}

func sampleModels(models []string) []string {
	normalized := make([]string, 0, len(models))
	for _, model := range models {
		if trimmed := strings.TrimSpace(model); trimmed != "" {
			normalized = append(normalized, trimmed)
		}
	}
	sort.Strings(normalized)
	if len(normalized) > modelSampleLimit {
		normalized = normalized[:modelSampleLimit]
	}
	return normalized
}

func sampleAllModels(models []string) []string {
	normalized := make([]string, 0, len(models))
	seen := make(map[string]struct{}, len(models))
	for _, model := range models {
		trimmed := strings.TrimSpace(model)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		normalized = append(normalized, trimmed)
	}
	sort.Strings(normalized)
	return normalized
}
