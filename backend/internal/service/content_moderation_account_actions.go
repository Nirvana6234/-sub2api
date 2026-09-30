package service

import (
	"context"
	"log/slog"
	"strings"
	"sync/atomic"
)

// ContentModerationAccountActions 是审核命中后在账号上的动作：累计窗口内的违规次数、到阈值自动封号、
// 命中与封号通知邮件。
//
// 单机与主节点用本机实现（就是原来的代码）；主从分流的从节点装经主节点执行的实现（设计 3.4）：违规次数要把
// 主节点和所有从节点的加在一起算，封号、发邮件（SMTP）也只在主节点。判定和记录仍在从节点。
type ContentModerationAccountActions interface {
	// Apply 在写审核记录之前调用：累计违规次数、按阈值封号，填 log.ViolationCount、log.AutoBanned，
	// 返回是否这一次刚封号。
	Apply(ctx context.Context, cfg *ContentModerationConfig, log *ContentModerationLog) bool
	// Notify 发通知邮件：cyber 为 true 时发 cyber 策略通知，否则按配置的"命中即通知"发；刚封号时另发封号通知。
	// 返回是否发出了任意一封。
	Notify(ctx context.Context, cfg *ContentModerationConfig, log *ContentModerationLog, autoBanJustApplied, cyber bool) bool
}

type contentModerationAccountActionsHolder struct{ a ContentModerationAccountActions }

// contentModerationAccountActionsSlot 嵌进审核服务：从节点装远程实现；没装时用本机实现。
type contentModerationAccountActionsSlot struct {
	accountActionsOverride atomic.Pointer[contentModerationAccountActionsHolder]
}

// SetAccountActions 换掉命中后的账号动作（从节点装配用）；nil 恢复本机实现。
func (s *contentModerationAccountActionsSlot) SetAccountActions(a ContentModerationAccountActions) {
	if a == nil {
		s.accountActionsOverride.Store(nil)
		return
	}
	s.accountActionsOverride.Store(&contentModerationAccountActionsHolder{a: a})
}

func (s *ContentModerationService) accountActions() ContentModerationAccountActions {
	if h := s.accountActionsOverride.Load(); h != nil {
		return h.a
	}
	return localContentModerationAccountActions{s: s}
}

// LocalAccountActions 返回本机实现（主节点执行从节点上报的违规时用，与单机同一段代码）。
func (s *ContentModerationService) LocalAccountActions() ContentModerationAccountActions {
	return localContentModerationAccountActions{s: s}
}

// localContentModerationAccountActions 是本机实现：库里的违规记录、用户仓储、邮件服务。
type localContentModerationAccountActions struct{ s *ContentModerationService }

func (l localContentModerationAccountActions) Apply(ctx context.Context, cfg *ContentModerationConfig, log *ContentModerationLog) bool {
	return l.s.applyFlaggedAccountSideEffects(ctx, cfg, log)
}

func (l localContentModerationAccountActions) Notify(ctx context.Context, cfg *ContentModerationConfig, log *ContentModerationLog, autoBanJustApplied, cyber bool) bool {
	s := l.s
	if !cyber {
		return s.sendFlaggedNotificationSideEffects(ctx, cfg, log, autoBanJustApplied)
	}
	emailSent := false
	if s.emailService != nil && strings.TrimSpace(log.UserEmail) != "" {
		if err := s.sendCyberPolicyEmail(ctx, log); err != nil {
			slog.Warn("content_moderation.cyber_email_failed", "user_id", contentModerationEmailUserID(log), "error", err)
		} else {
			emailSent = true
		}
		if autoBanJustApplied {
			if err := s.sendAccountDisabledEmail(ctx, cfg, log); err != nil {
				slog.Warn("content_moderation.cyber_ban_email_failed", "user_id", contentModerationEmailUserID(log), "error", err)
			} else {
				emailSent = true
			}
		}
	}
	return emailSent
}
