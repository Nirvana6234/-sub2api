package node

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/relay/nodestore"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

// 本机存储里审核记录的种类：命中与未命中分开放（保留期不同，按种类整天清理）；邮件已发的回填另记一条。
const (
	moderationHitKind   = "moderation-hit"
	moderationOtherKind = "moderation-other"
	moderationPatchKind = "moderation-patch"
)

// errModerationCountOnMaster：累计违规次数在主节点（从节点装的是 RemoteModerationActions，不会走到这里）。
var errModerationCountOnMaster = errors.New("violation counts are kept on the master")

// ModerationStore 是从节点上的审核记录仓储（service.ContentModerationRepository，设计 3.4、第 12 节）：
// 记录留在本机数据目录，重启不丢，按单机的保留期清理；后台按节点查询（WP14）用 ListLogs。
type ModerationStore struct {
	store  *nodestore.Store
	lastID atomic.Int64
	now    func() time.Time
}

var _ service.ContentModerationRepository = (*ModerationStore)(nil)

// NewModerationStore 创建仓储。
func NewModerationStore(store *nodestore.Store) *ModerationStore {
	return &ModerationStore{store: store, now: time.Now}
}

type moderationPatch struct {
	ID        int64 `json:"id"`
	EmailSent bool  `json:"email_sent"`
}

// nextID 是本机唯一的记录 ID（按时间递增；只在这台节点上有意义）。
func (m *ModerationStore) nextID() int64 {
	for {
		now := m.now().UnixNano()
		last := m.lastID.Load()
		if now <= last {
			now = last + 1
		}
		if m.lastID.CompareAndSwap(last, now) {
			return now
		}
	}
}

func (m *ModerationStore) CreateLog(_ context.Context, log *service.ContentModerationLog) error {
	if log == nil {
		return nil
	}
	log.ID = m.nextID()
	log.CreatedAt = m.now()
	kind := moderationOtherKind
	if log.Flagged {
		kind = moderationHitKind
	}
	return m.store.Append(kind, log)
}

func (m *ModerationStore) UpdateLogEmailSent(_ context.Context, id int64, sent bool) error {
	return m.store.Append(moderationPatchKind, moderationPatch{ID: id, EmailSent: sent})
}

func (m *ModerationStore) CountFlaggedByUserSince(context.Context, int64, time.Time, bool) (int, error) {
	return 0, errModerationCountOnMaster
}

func (m *ModerationStore) CleanupExpiredLogs(_ context.Context, hitBefore, nonHitBefore time.Time) (*service.ContentModerationCleanupResult, error) {
	hit, err := m.store.DeleteBefore(moderationHitKind, hitBefore)
	if err != nil {
		return nil, err
	}
	other, err := m.store.DeleteBefore(moderationOtherKind, nonHitBefore)
	if err != nil {
		return nil, err
	}
	if _, err := m.store.DeleteBefore(moderationPatchKind, hitBefore); err != nil {
		return nil, err
	}
	return &service.ContentModerationCleanupResult{DeletedHit: hit, DeletedNonHit: other, FinishedAt: m.now()}, nil
}

// ListLogs 按单机同样的筛选条件列出（新到旧、分页）。
func (m *ModerationStore) ListLogs(_ context.Context, filter service.ContentModerationLogFilter) ([]service.ContentModerationLog, *pagination.PaginationResult, error) {
	var from, to time.Time
	if filter.From != nil {
		from = *filter.From
	}
	if filter.To != nil {
		to = *filter.To
	}
	sent := map[int64]bool{}
	if err := m.store.Scan(moderationPatchKind, from, time.Time{}, func(line []byte) bool {
		var p moderationPatch
		if json.Unmarshal(line, &p) == nil {
			if _, seen := sent[p.ID]; !seen {
				sent[p.ID] = p.EmailSent
			}
		}
		return true
	}); err != nil {
		return nil, nil, err
	}
	var matched []service.ContentModerationLog
	collect := func(kind string) error {
		return m.store.Scan(kind, from, to, func(line []byte) bool {
			var l service.ContentModerationLog
			if json.Unmarshal(line, &l) != nil || !moderationLogMatches(&l, filter) {
				return true
			}
			if v, ok := sent[l.ID]; ok {
				l.EmailSent = v
			}
			matched = append(matched, l)
			return true
		})
	}
	result := strings.ToLower(strings.TrimSpace(filter.Result))
	if result != "pass" && result != "allow" {
		if err := collect(moderationHitKind); err != nil {
			return nil, nil, err
		}
	}
	if result != "hit" && result != "flagged" && result != "blocked" && result != "block" {
		if err := collect(moderationOtherKind); err != nil {
			return nil, nil, err
		}
	}
	sortModerationLogsNewestFirst(matched)
	page, size := filter.Pagination.Page, filter.Pagination.PageSize
	if page <= 0 {
		page = 1
	}
	if size <= 0 {
		size = 20
	}
	total := int64(len(matched))
	start := (page - 1) * size
	if start > len(matched) {
		start = len(matched)
	}
	end := start + size
	if end > len(matched) {
		end = len(matched)
	}
	pages := int((total + int64(size) - 1) / int64(size))
	return matched[start:end], &pagination.PaginationResult{Total: total, Page: page, PageSize: size, Pages: pages}, nil
}

// moderationLogMatches 与仓储 buildContentModerationLogWhere 的条件相同。
func moderationLogMatches(l *service.ContentModerationLog, f service.ContentModerationLogFilter) bool {
	switch strings.ToLower(strings.TrimSpace(f.Result)) {
	case "hit", "flagged":
		if !l.Flagged {
			return false
		}
	case "blocked", "block":
		if l.Action != "block" && l.Action != "keyword_block" && l.Action != "hash_block" {
			return false
		}
	case "pass", "allow":
		if l.Flagged || l.Error != "" {
			return false
		}
	case "error":
		if l.Error == "" {
			return false
		}
	}
	if f.GroupID != nil && (l.GroupID == nil || *l.GroupID != *f.GroupID) {
		return false
	}
	if e := strings.TrimSpace(f.Endpoint); e != "" && l.Endpoint != e {
		return false
	}
	if q := strings.ToLower(strings.TrimSpace(f.Search)); q != "" {
		hay := strings.ToLower(l.RequestID + "\n" + l.UserEmail + "\n" + l.APIKeyName + "\n" + l.Model + "\n" + l.InputExcerpt)
		if !strings.Contains(hay, q) {
			return false
		}
	}
	if f.From != nil && !f.From.IsZero() && l.CreatedAt.Before(*f.From) {
		return false
	}
	if f.To != nil && !f.To.IsZero() && l.CreatedAt.After(*f.To) {
		return false
	}
	return true
}

// sortModerationLogsNewestFirst：两个种类各自已是新到旧，合并后按 ID（时间）排一次。
func sortModerationLogsNewestFirst(logs []service.ContentModerationLog) {
	sort.Slice(logs, func(i, j int) bool { return logs[i].ID > logs[j].ID })
}
