package securityaudit

import (
	"encoding/json"
	"errors"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// PreparedSegment 是从请求体里按协议抽出的一段提示词（promptSegment 的可传输形式）。
type PreparedSegment struct {
	Text string `json:"text"`
	User bool   `json:"user,omitempty"`
	Role string `json:"role,omitempty"`
}

// PreparedInput 是从请求体预先抽出的审核输入：提示词审计要的分段、内容审核要的文字和图片。
// 主从分流时从节点用同一段抽取代码算好发给主节点，主节点不拿请求体（设计 3.4：请求体可能很大，
// 审核只看这些；提示词审计存的证据本来就是抽出的文字）。
type PreparedInput struct {
	Segments []PreparedSegment `json:"segments,omitempty"`
	// InvalidJSON：请求体不是合法 JSON（提示词审计按"JSON 无效"处理，与拿请求体时一样）。
	InvalidJSON bool                           `json:"invalid_json,omitempty"`
	Moderation  service.ContentModerationInput `json:"moderation"`
}

// PrepareRequest 用请求体算好审核输入并去掉请求体。结果交给 Check 与拿原请求体判定一致（有等价测试）。
func PrepareRequest(req Request) Request {
	prepared := &PreparedInput{Moderation: service.ExtractContentModerationInput(req.Protocol, req.Body).ForTransfer()}
	var document any
	if err := json.Unmarshal(req.Body, &document); err != nil {
		prepared.InvalidJSON = true
	} else {
		for _, s := range extractProtocolSegments(req.Protocol, document) {
			prepared.Segments = append(prepared.Segments, PreparedSegment{Text: s.text, User: s.user, Role: s.role})
		}
	}
	req.Body = nil
	req.Prepared = prepared
	return req
}

var errInvalidPromptJSON = errors.New("prompt audit request JSON is invalid")

// requestSegments 取提示词分段：预先抽好的优先（请求体已去掉），否则从请求体抽。
func requestSegments(req Request) ([]promptSegment, error) {
	if req.Prepared != nil && len(req.Body) == 0 {
		if req.Prepared.InvalidJSON {
			return nil, errInvalidPromptJSON
		}
		out := make([]promptSegment, 0, len(req.Prepared.Segments))
		for _, s := range req.Prepared.Segments {
			out = append(out, promptSegment{text: s.Text, user: s.User, role: s.Role})
		}
		return out, nil
	}
	var document any
	if err := json.Unmarshal(req.Body, &document); err != nil {
		return nil, errInvalidPromptJSON
	}
	return extractProtocolSegments(req.Protocol, document), nil
}

// preparedModeration 是交给内容审核的预先抽好的输入（没有时为 nil，内容审核从请求体抽）。
func preparedModeration(req Request) *service.ContentModerationInput {
	if req.Prepared == nil || len(req.Body) != 0 {
		return nil
	}
	in := req.Prepared.Moderation
	return &in
}

// AllowDecision 是放行的判定（不会被审计时）。
func AllowDecision() Decision { return allowDecision(nil, nil) }

// UnavailableDecision 是审计判定不可用时的结果（与提示词审计阻断模式下审计服务出错时一样：503，不往下走）。
func UnavailableDecision() Decision {
	return prioritize(nil, unavailablePromptDecision(ErrorCodeUnavailable))
}
