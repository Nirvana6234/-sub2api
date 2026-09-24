package service

import (
	"encoding/json"
	"strings"
	"unicode/utf8"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

// 试用只开放「纯文字聊天」。请求体按白名单重建，而不是按黑名单剔除：
// 未列出的字段一律丢弃，工具/多模态相关字段显式报错，免得访客以为功能可用。
var (
	ErrGuestTrialTextOnly       = infraerrors.BadRequest("GUEST_TRIAL_TEXT_ONLY", "试用版仅支持文字聊天，注册后可生图、改图和上传文件")
	ErrGuestTrialModelNotAllowed = infraerrors.BadRequest("GUEST_TRIAL_MODEL_NOT_ALLOWED", "该模型不在试用范围内")
	ErrGuestTrialInputTooLong   = infraerrors.BadRequest("GUEST_TRIAL_INPUT_TOO_LONG", "内容太长了，试用版单次对话有字数上限")
	ErrGuestTrialInvalidRequest = infraerrors.BadRequest("GUEST_TRIAL_INVALID_REQUEST", "请求格式不正确")
)

const guestTrialMaxMessages = 50

// guestTrialPassthroughFields 原样保留的采样参数（值由上游自行校验）。
var guestTrialPassthroughFields = []string{"temperature", "top_p", "presence_penalty", "frequency_penalty", "stop", "stream"}

// guestTrialForbiddenFields 出现即拒绝：它们意味着工具调用、多模态输出或批量生成。
var guestTrialForbiddenFields = []string{
	"tools", "tool_choice", "functions", "function_call", "parallel_tool_calls",
	"modalities", "audio", "prediction", "web_search_options", "n",
}

var guestTrialAllowedRoles = map[string]bool{"system": true, "user": true, "assistant": true}

type guestTrialMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

type guestTrialContentPart struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// SanitizeGuestTrialChatBody 校验并重建试用聊天请求体。
// 返回的 body 只含白名单字段，模型取白名单里的规范写法，输出上限被强制写入。
func SanitizeGuestTrialChatBody(body []byte, cfg *GuestTrialConfig) ([]byte, string, error) {
	if cfg == nil {
		return nil, "", ErrGuestTrialInvalidRequest
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil || fields == nil {
		return nil, "", ErrGuestTrialInvalidRequest
	}
	for _, field := range guestTrialForbiddenFields {
		if _, exists := fields[field]; exists {
			return nil, "", ErrGuestTrialTextOnly
		}
	}

	var model string
	if raw, ok := fields["model"]; !ok || json.Unmarshal(raw, &model) != nil {
		model = cfg.DefaultModel()
	}
	model = strings.TrimSpace(model)
	if model == "" {
		model = cfg.DefaultModel()
	}
	canonicalModel := ""
	for _, allowed := range cfg.Models {
		if strings.EqualFold(allowed, model) {
			canonicalModel = allowed
			break
		}
	}
	if canonicalModel == "" {
		return nil, "", ErrGuestTrialModelNotAllowed
	}

	var messages []guestTrialMessage
	if raw, ok := fields["messages"]; !ok || json.Unmarshal(raw, &messages) != nil || len(messages) == 0 {
		return nil, "", ErrGuestTrialInvalidRequest
	}
	if len(messages) > guestTrialMaxMessages {
		return nil, "", ErrGuestTrialInputTooLong
	}

	cleaned := make([]map[string]string, 0, len(messages))
	totalChars := 0
	for _, message := range messages {
		role := strings.ToLower(strings.TrimSpace(message.Role))
		if !guestTrialAllowedRoles[role] {
			return nil, "", ErrGuestTrialTextOnly
		}
		text, err := guestTrialMessageText(message.Content)
		if err != nil {
			return nil, "", err
		}
		totalChars += utf8.RuneCountInString(text)
		if totalChars > cfg.MaxInputChars {
			return nil, "", ErrGuestTrialInputTooLong
		}
		cleaned = append(cleaned, map[string]string{"role": role, "content": text})
	}

	out := map[string]any{
		"model":                 canonicalModel,
		"messages":              cleaned,
		"max_tokens":            cfg.MaxOutputTokens,
		"max_completion_tokens": cfg.MaxOutputTokens,
	}
	for _, field := range guestTrialPassthroughFields {
		if raw, exists := fields[field]; exists {
			out[field] = raw
		}
	}
	// 流式时要求上游回传用量，保证试用消耗能被正确记账。
	if stream, ok := fields["stream"]; ok && strings.TrimSpace(string(stream)) == "true" {
		out["stream_options"] = map[string]bool{"include_usage": true}
	}
	rebuilt, err := json.Marshal(out)
	if err != nil {
		return nil, "", ErrGuestTrialInvalidRequest
	}
	return rebuilt, canonicalModel, nil
}

// guestTrialMessageText 把消息内容压成纯文本：字符串直接用；数组只允许 text 片段。
func guestTrialMessageText(raw json.RawMessage) (string, error) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return "", nil
	}
	if strings.HasPrefix(trimmed, "\"") {
		var text string
		if err := json.Unmarshal(raw, &text); err != nil {
			return "", ErrGuestTrialInvalidRequest
		}
		return text, nil
	}
	var parts []guestTrialContentPart
	if err := json.Unmarshal(raw, &parts); err != nil {
		return "", ErrGuestTrialInvalidRequest
	}
	var builder strings.Builder
	for _, part := range parts {
		if part.Type != "text" {
			return "", ErrGuestTrialTextOnly
		}
		builder.WriteString(part.Text)
	}
	return builder.String(), nil
}
