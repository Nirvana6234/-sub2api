package service

import (
	"context"
	"errors"
)

// ContentModerationHashChange 是命中过的输入名单的一次变化（主从分流时主节点把它推给各从节点的副本，设计 3.4）。
type ContentModerationHashChange struct {
	Added   []string
	Removed []string
	// Cleared：后台清空了名单（先于 Added 生效）。
	Cleared bool
}

// ContentModerationHashScanner 是能分页列出名单的哈希缓存（Redis 实现有；从节点整份拉取副本时用）。
type ContentModerationHashScanner interface {
	ScanFlaggedInputHashes(ctx context.Context, cursor uint64, count int64) ([]string, uint64, error)
}

// ErrContentModerationHashScanUnsupported：哈希缓存不能分页列出。
var ErrContentModerationHashScanUnsupported = errors.New("content moderation hash cache cannot be scanned")

// InvalidateRuntimeSnapshot 丢掉缓存的审核配置，下一次判定重新读取（从节点换配置快照时调用：
// 主节点下发的新配置当场生效，不等缓存过期）。
func (s *ContentModerationService) InvalidateRuntimeSnapshot() {
	if s == nil {
		return
	}
	s.runtimeRefreshMu.Lock()
	s.runtimeSnapshot.Store(nil)
	s.runtimeRefreshMu.Unlock()
}

// SetHashChangeListener 设置名单变化的回调（主节点的主从分流运行时用）；nil 取消。
func (s *ContentModerationService) SetHashChangeListener(fn func(ContentModerationHashChange)) {
	if fn == nil {
		s.hashChanges.Store(nil)
		return
	}
	s.hashChanges.Store(&fn)
}

func (s *ContentModerationService) publishHashChange(ch ContentModerationHashChange) {
	if s == nil {
		return
	}
	if fn := s.hashChanges.Load(); fn != nil {
		(*fn)(ch)
	}
}

// RecordRelayFlaggedHash 记下从节点新命中的输入（主节点执行），并推给所有节点。
func (s *ContentModerationService) RecordRelayFlaggedHash(ctx context.Context, hash string) error {
	hash = normalizeContentModerationHash(hash)
	if hash == "" {
		return errors.New("invalid content moderation hash")
	}
	if s == nil || s.hashCache == nil {
		return nil
	}
	if err := s.hashCache.RecordFlaggedInputHash(ctx, hash); err != nil {
		return err
	}
	s.publishHashChange(ContentModerationHashChange{Added: []string{hash}})
	return nil
}

// ScanFlaggedInputHashes 分页列出命中过的输入名单（next 为 0 表示列完）。
func (s *ContentModerationService) ScanFlaggedInputHashes(ctx context.Context, cursor uint64, count int64) ([]string, uint64, error) {
	if s == nil || s.hashCache == nil {
		return nil, 0, nil
	}
	scanner, ok := s.hashCache.(ContentModerationHashScanner)
	if !ok {
		return nil, 0, ErrContentModerationHashScanUnsupported
	}
	return scanner.ScanFlaggedInputHashes(ctx, cursor, count)
}
