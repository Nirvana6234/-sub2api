package node

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/relay/proto/relayv1"
	"github.com/Wei-Shaw/sub2api/internal/relay/transport"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

// ErrFlaggedHashesNotReady：副本还没整份拉取过。审核服务按单机"哈希检查出错"处理（记警告、继续往下审）。
var ErrFlaggedHashesNotReady = errors.New("relay node has not fetched the flagged input list yet")

// errFlaggedHashesMasterOnly：名单的删除、清空、计数是后台操作，只在主节点。
var errFlaggedHashesMasterOnly = errors.New("flagged input list is managed on the master")

// FlaggedHashReplica 是从节点上"命中过的输入"名单的副本（service.ContentModerationHashCache，设计 3.4）：
// 主节点的 Redis 名单是权威。每次连上事件流后整份拉取（Resync），之后按主节点推来的增量更新（Apply）；
// 这台新命中的输入先记进副本，再报给主节点，主节点写入后推给所有节点。
type FlaggedHashReplica struct {
	control relayv1.RelayControlClient

	mu    sync.RWMutex
	set   map[string]struct{}
	ready bool
	// resyncing 时收到的增量另记一份，整份拉完后补上（拉取期间的变化不能丢）。
	resyncing bool
	during    []*relayv1.FlaggedHashes
}

var _ service.ContentModerationHashCache = (*FlaggedHashReplica)(nil)

// NewFlaggedHashReplica 创建副本（控制连接）。
func NewFlaggedHashReplica(client *transport.Client) *FlaggedHashReplica {
	return newFlaggedHashReplica(relayv1.NewRelayControlClient(client.Conn(transport.TierControl)))
}

func newFlaggedHashReplica(control relayv1.RelayControlClient) *FlaggedHashReplica {
	return &FlaggedHashReplica{control: control, set: map[string]struct{}{}}
}

// HasFlaggedInputHash 查副本。
func (r *FlaggedHashReplica) HasFlaggedInputHash(_ context.Context, inputHash string) (bool, error) {
	inputHash = strings.TrimSpace(inputHash)
	if inputHash == "" {
		return false, nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if !r.ready {
		return false, ErrFlaggedHashesNotReady
	}
	_, ok := r.set[inputHash]
	return ok, nil
}

// RecordFlaggedInputHash 记下这台新命中的输入：先进副本（这台当场生效），再报给主节点。
func (r *FlaggedHashReplica) RecordFlaggedInputHash(ctx context.Context, inputHash string) error {
	inputHash = strings.TrimSpace(inputHash)
	if inputHash == "" {
		return nil
	}
	r.mu.Lock()
	r.set[inputHash] = struct{}{}
	if r.resyncing {
		r.during = append(r.during, &relayv1.FlaggedHashes{Added: []string{inputHash}})
	}
	r.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), moderationCallTimeout)
	defer cancel()
	_, err := r.control.RecordFlaggedHash(ctx, &relayv1.RecordFlaggedHashRequest{Hash: inputHash})
	return err
}

func (r *FlaggedHashReplica) DeleteFlaggedInputHash(context.Context, string) (bool, error) {
	return false, errFlaggedHashesMasterOnly
}

func (r *FlaggedHashReplica) ClearFlaggedInputHashes(context.Context) (int64, error) {
	return 0, errFlaggedHashesMasterOnly
}

func (r *FlaggedHashReplica) CountFlaggedInputHashes(context.Context) (int64, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return int64(len(r.set)), nil
}

// Apply 应用主节点推来的增量。
func (r *FlaggedHashReplica) Apply(ch *relayv1.FlaggedHashes) {
	if ch == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	applyFlaggedHashes(r.set, ch)
	if r.resyncing {
		r.during = append(r.during, ch)
	}
}

func applyFlaggedHashes(set map[string]struct{}, ch *relayv1.FlaggedHashes) {
	if ch.GetCleared() {
		for k := range set {
			delete(set, k)
		}
	}
	for _, h := range ch.GetRemoved() {
		delete(set, h)
	}
	for _, h := range ch.GetAdded() {
		set[h] = struct{}{}
	}
}

// Resync 整份拉取名单并换上（每次连上事件流后调用：断线期间的增量可能错过了）。
func (r *FlaggedHashReplica) Resync(ctx context.Context) error {
	r.mu.Lock()
	r.resyncing, r.during = true, nil
	r.mu.Unlock()
	next := map[string]struct{}{}
	var cursor uint64
	err := func() error {
		for {
			cctx, cancel := context.WithTimeout(ctx, moderationCallTimeout)
			resp, err := r.control.FetchFlaggedHashes(cctx, &relayv1.FetchFlaggedHashesRequest{Cursor: cursor})
			cancel()
			if err != nil {
				return err
			}
			for _, h := range resp.GetHashes() {
				next[h] = struct{}{}
			}
			if cursor = resp.GetNextCursor(); cursor == 0 {
				return nil
			}
		}
	}()
	r.mu.Lock()
	defer r.mu.Unlock()
	during := r.during
	r.resyncing, r.during = false, nil
	if err != nil {
		return err
	}
	// 拉取期间主节点推来的增量、这台自己新记的，按先后补到新名单上（它们可能晚于拉到的那一页）。
	// 旧副本里的其余内容不保留：断线期间错过的删除、清空要以这次拉到的为准。
	for _, ch := range during {
		applyFlaggedHashes(next, ch)
	}
	r.set, r.ready = next, true
	return nil
}

// RunResyncOnConnect 在每次连上事件流后整份拉取（失败时 5 秒后重试，直到成功或 ctx 结束）。
func (r *FlaggedHashReplica) RunResyncOnConnect(ctx context.Context) {
	for {
		err := r.Resync(ctx)
		if err == nil || ctx.Err() != nil {
			return
		}
		slog.Warn("relay flagged input list fetch failed", "error", err)
		select {
		case <-ctx.Done():
			return
		case <-time.After(5 * time.Second):
		}
	}
}
