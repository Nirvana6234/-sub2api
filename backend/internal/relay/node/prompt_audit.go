package node

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/relay/nodestore"
	"github.com/Wei-Shaw/sub2api/internal/securityaudit"
)

// promptAuditEventKind 是本机存储里提示词审计事件的种类。
const promptAuditEventKind = "prompt-audit-events"

// errPromptAuditEventsOnNode：审计事件的删除是后台操作，从节点上按保留期清理，不单条删。
var errPromptAuditEventsOnNode = errors.New("prompt audit events on relay nodes are managed by retention, not deleted individually")

// PromptAuditStore 是从节点上的提示词审计存储（securityaudit.PromptRepository，设计 3.4）：
// 异步审计的任务队列放内存，状态流转与 PostgreSQL 实现相同（暂存 → 排队 → 处理 → 完成 / 重试 / 失败），
// 容量按配置的队列上限；审计事件写本机存储（重启不丢，后台按节点查询）。
//
// 与单机的差异：队列只在内存，从节点重启时还没审完的异步任务丢掉（单机的队列在数据库里，重启后接着审）。
// 异步审计本来就是尽力而为（入队忙时同样丢弃），阻断模式不经队列，不受影响。
type PromptAuditStore struct {
	store  *nodestore.Store
	now    func() time.Time
	lastID atomic.Int64

	mu   sync.Mutex
	jobs map[int64]*securityaudit.Job
}

var _ securityaudit.PromptRepository = (*PromptAuditStore)(nil)

// NewPromptAuditStore 创建存储。
func NewPromptAuditStore(store *nodestore.Store) *PromptAuditStore {
	return &PromptAuditStore{store: store, now: time.Now, jobs: map[int64]*securityaudit.Job{}}
}

func (p *PromptAuditStore) nextID() int64 {
	for {
		now := p.now().UnixNano()
		last := p.lastID.Load()
		if now <= last {
			now = last + 1
		}
		if p.lastID.CompareAndSwap(last, now) {
			return now
		}
	}
}

func activeStatus(status string) bool {
	switch status {
	case "staging", "queued", "processing", "retry":
		return true
	}
	return false
}

func cloneJob(j *securityaudit.Job) *securityaudit.Job {
	cp := *j
	return &cp
}

func (p *PromptAuditStore) CreateStagingWithCapacity(_ context.Context, snapshot securityaudit.PromptSnapshot, configVersion int64, maxAttempts, capacity int) (*securityaudit.Job, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	active := 0
	for _, j := range p.jobs {
		if activeStatus(j.Status) {
			active++
		}
	}
	if capacity > 0 && active >= capacity {
		return nil, securityaudit.ErrQueueFull
	}
	now := p.now()
	job := &securityaudit.Job{
		ID: p.nextID(), Snapshot: snapshot.Redacted(), ExecutionMode: securityaudit.ModeAsync, ConfigVersion: configVersion,
		Status: "staging", MaxAttempts: maxAttempts, NextAttemptAt: now, CreatedAt: now, UpdatedAt: now,
	}
	p.jobs[job.ID] = job
	return cloneJob(job), nil
}

func (p *PromptAuditStore) PublishQueued(_ context.Context, jobID int64) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	j, ok := p.jobs[jobID]
	if !ok || j.Status != "staging" {
		return securityaudit.ErrLeaseLost
	}
	j.Status, j.UpdatedAt = "queued", p.now()
	return nil
}

func (p *PromptAuditStore) MarkStagingFailed(_ context.Context, jobID int64, code, message string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if j, ok := p.jobs[jobID]; ok && j.Status == "staging" {
		delete(p.jobs, jobID)
		_ = code
		_ = message
	}
	return nil
}

// ClaimNextJob 领最早一个到期的排队或重试任务（与 PostgreSQL 实现相同的顺序：按到期时间、再按 ID）。
func (p *PromptAuditStore) ClaimNextJob(_ context.Context, now time.Time) (*securityaudit.Job, bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	var picked *securityaudit.Job
	for _, j := range p.jobs {
		if (j.Status != "queued" && j.Status != "retry") || j.NextAttemptAt.After(now) {
			continue
		}
		if picked == nil || j.NextAttemptAt.Before(picked.NextAttemptAt) || (j.NextAttemptAt.Equal(picked.NextAttemptAt) && j.ID < picked.ID) {
			picked = j
		}
	}
	if picked == nil {
		return nil, false, nil
	}
	started := now
	picked.Status, picked.Attempts, picked.ClaimVersion = "processing", picked.Attempts+1, picked.ClaimVersion+1
	picked.ProcessingStartedAt, picked.UpdatedAt = &started, now
	return cloneJob(picked), true, nil
}

func (p *PromptAuditStore) leased(jobID, claimVersion int64) (*securityaudit.Job, error) {
	j, ok := p.jobs[jobID]
	if !ok || j.Status != "processing" || j.ClaimVersion != claimVersion {
		return nil, securityaudit.ErrLeaseLost
	}
	return j, nil
}

func (p *PromptAuditStore) RefreshLease(_ context.Context, jobID, claimVersion int64, now time.Time) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	j, err := p.leased(jobID, claimVersion)
	if err != nil {
		return err
	}
	started := now
	j.ProcessingStartedAt, j.UpdatedAt = &started, now
	return nil
}

func (p *PromptAuditStore) Complete(_ context.Context, job *securityaudit.Job, result *securityaudit.NormalizedResult, storePassEvents bool) (*securityaudit.Event, error) {
	if job == nil || result == nil {
		return nil, errors.New("prompt audit completion requires job and result")
	}
	p.mu.Lock()
	if _, err := p.leased(job.ID, job.ClaimVersion); err != nil {
		p.mu.Unlock()
		return nil, err
	}
	delete(p.jobs, job.ID)
	p.mu.Unlock()
	if !securityaudit.ShouldStoreEvent(result.Decision, storePassEvents) {
		return nil, nil
	}
	return p.appendEvent(job.ID, job.Snapshot, job.ConfigVersion, result)
}

func (p *PromptAuditStore) appendEvent(jobID int64, snapshot securityaudit.PromptSnapshot, configVersion int64, result *securityaudit.NormalizedResult) (*securityaudit.Event, error) {
	event := securityaudit.BuildEvent(p.nextID(), jobID, snapshot.Redacted(), configVersion, result, p.now())
	if err := p.store.Append(promptAuditEventKind, event); err != nil {
		return nil, err
	}
	return event, nil
}

func (p *PromptAuditStore) Retry(_ context.Context, jobID, claimVersion int64, next time.Time, code, message string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	j, err := p.leased(jobID, claimVersion)
	if err != nil {
		return err
	}
	j.Status, j.NextAttemptAt, j.ProcessingStartedAt = "retry", next, nil
	j.LastErrorCode, j.LastErrorMessage, j.UpdatedAt = code, message, p.now()
	return nil
}

func (p *PromptAuditStore) Fail(_ context.Context, jobID, claimVersion int64, _, _ string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, err := p.leased(jobID, claimVersion); err != nil {
		return err
	}
	delete(p.jobs, jobID)
	return nil
}

// ReclaimStale 与 PostgreSQL 实现相同：暂存太久的判失败；处理太久（租约过期）的还有次数就重试、否则失败。
func (p *PromptAuditStore) ReclaimStale(_ context.Context, stagingBefore, processingBefore time.Time, limit int) (int64, error) {
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	now := p.now()
	var n int64
	for id, j := range p.jobs {
		if int(n) >= limit {
			break
		}
		switch {
		case j.Status == "staging" && j.UpdatedAt.Before(stagingBefore):
			delete(p.jobs, id)
		case j.Status == "processing" && j.ProcessingStartedAt != nil && j.ProcessingStartedAt.Before(processingBefore):
			if j.Attempts < j.MaxAttempts {
				j.Status, j.NextAttemptAt = "retry", now
			} else {
				delete(p.jobs, id)
			}
			j.ProcessingStartedAt, j.LastErrorCode, j.UpdatedAt = nil, "processing_lease_expired", now
		default:
			continue
		}
		n++
	}
	return n, nil
}

func (p *PromptAuditStore) QueueStats(context.Context) (securityaudit.QueueStats, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	var stats securityaudit.QueueStats
	for _, j := range p.jobs {
		switch j.Status {
		case "staging":
			stats.Staging++
		case "queued":
			stats.Queued++
		case "processing":
			stats.Processing++
		case "retry":
			stats.Retry++
		}
	}
	return stats, nil
}

// RecordBlocking 记下一次阻断模式的审计（不经队列，直接写事件）。
func (p *PromptAuditStore) RecordBlocking(_ context.Context, snapshot securityaudit.PromptSnapshot, configVersion int64, result *securityaudit.NormalizedResult, storePassEvents bool) (*securityaudit.Event, error) {
	if result == nil {
		return nil, errors.New("prompt guard result required")
	}
	if !securityaudit.ShouldStoreEvent(result.Decision, storePassEvents) {
		return nil, nil
	}
	return p.appendEvent(p.nextID(), snapshot, configVersion, result)
}

// ---- 审计事件（本机存储；后台按节点查询用 ListEvents / GetEvent）----

func (p *PromptAuditStore) ListEvents(_ context.Context, _ securityaudit.EventFilter, page, pageSize int) (*securityaudit.EventPage, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}
	var all []*securityaudit.Event
	if err := p.store.Scan(promptAuditEventKind, time.Time{}, time.Time{}, func(line []byte) bool {
		var e securityaudit.Event
		if json.Unmarshal(line, &e) == nil {
			all = append(all, &e)
		}
		return true
	}); err != nil {
		return nil, err
	}
	sort.Slice(all, func(i, j int) bool { return all[i].ID > all[j].ID })
	start := (page - 1) * pageSize
	if start > len(all) {
		start = len(all)
	}
	end := start + pageSize
	if end > len(all) {
		end = len(all)
	}
	pages := (len(all) + pageSize - 1) / pageSize
	return &securityaudit.EventPage{Items: all[start:end], Total: int64(len(all)), Page: page, PageSize: pageSize, Pages: pages}, nil
}

func (p *PromptAuditStore) GetEvent(_ context.Context, id int64) (*securityaudit.Event, error) {
	var found *securityaudit.Event
	err := p.store.Scan(promptAuditEventKind, time.Time{}, time.Time{}, func(line []byte) bool {
		var e securityaudit.Event
		if json.Unmarshal(line, &e) == nil && e.ID == id {
			found = &e
			return false
		}
		return true
	})
	if err != nil {
		return nil, err
	}
	if found == nil {
		return nil, securityaudit.ErrEventNotFound
	}
	return found, nil
}

func (p *PromptAuditStore) DeleteEvent(context.Context, int64) (*securityaudit.DeleteResult, error) {
	return nil, errPromptAuditEventsOnNode
}

func (p *PromptAuditStore) DeleteEventsByIDs(context.Context, []int64) (*securityaudit.DeleteResult, error) {
	return nil, errPromptAuditEventsOnNode
}

func (p *PromptAuditStore) PreviewDelete(context.Context, securityaudit.EventFilter) (*securityaudit.DeletePreview, error) {
	return nil, errPromptAuditEventsOnNode
}

func (p *PromptAuditStore) DeleteEventsByFilter(context.Context, securityaudit.EventFilter, int64, int) (*securityaudit.DeleteResult, error) {
	return nil, errPromptAuditEventsOnNode
}

// PromptPayloads 是从节点上的待审内容存储（securityaudit.PayloadStore）：只在内存，按单机同样的有效期过期。
type PromptPayloads struct {
	mu    sync.Mutex
	items map[int64]promptPayload
	now   func() time.Time
}

type promptPayload struct {
	text    string
	expires time.Time
}

var _ securityaudit.PayloadStore = (*PromptPayloads)(nil)

// NewPromptPayloads 创建待审内容存储。
func NewPromptPayloads() *PromptPayloads {
	return &PromptPayloads{items: map[int64]promptPayload{}, now: time.Now}
}

func (s *PromptPayloads) Set(_ context.Context, jobID int64, scanText string, ttl time.Duration) error {
	if jobID <= 0 || scanText == "" {
		return errors.New("prompt audit payload input invalid")
	}
	if ttl <= 0 || ttl > securityaudit.DefaultPayloadTTL {
		ttl = securityaudit.DefaultPayloadTTL
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	for id, it := range s.items {
		if now.After(it.expires) {
			delete(s.items, id)
		}
	}
	s.items[jobID] = promptPayload{text: scanText, expires: now.Add(ttl)}
	return nil
}

func (s *PromptPayloads) Get(_ context.Context, jobID int64) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	it, ok := s.items[jobID]
	if !ok || s.now().After(it.expires) {
		delete(s.items, jobID)
		return "", errors.New("prompt audit payload expired")
	}
	return it.text, nil
}

func (s *PromptPayloads) Delete(_ context.Context, jobID int64) error {
	s.mu.Lock()
	delete(s.items, jobID)
	s.mu.Unlock()
	return nil
}

func (s *PromptPayloads) Ping(context.Context) error { return nil }

// sealedSectionPromptAudit 与 master.SealedSectionPromptAudit 相同。
const sealedSectionPromptAudit = "prompt_audit"

// PromptAuditConfig 返回从节点上提示词审计配置的读取函数（给 securityaudit.NewRelayConfigStore）：来自配置快照里
// 加密下发的部分，按快照版本解析一次。还没拿到快照时 ok 为 false（从节点这时也不接请求）。
func PromptAuditConfig(cache *ConfigCache) func() (securityaudit.RelayPromptConfig, bool) {
	type parsed struct {
		version string
		cfg     securityaudit.RelayPromptConfig
		ok      bool
	}
	var last atomic.Pointer[parsed]
	return func() (securityaudit.RelayPromptConfig, bool) {
		version := cache.Version()
		if p := last.Load(); p != nil && p.version == version {
			return p.cfg, p.ok
		}
		next := &parsed{version: version}
		if raw, ok := cache.SealedSection(sealedSectionPromptAudit); ok {
			if err := json.Unmarshal(raw, &next.cfg); err == nil {
				next.ok = true
			} else {
				// 解不开：按降级处理（阻断模式下拒绝放行，与主节点配置读不出时一样）。
				next.cfg, next.ok = securityaudit.RelayPromptConfig{Degraded: true}, true
			}
		}
		last.Store(next)
		return next.cfg, next.ok
	}
}
