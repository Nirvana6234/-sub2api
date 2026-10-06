package master

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/relay/proto/relayv1"
)

// 日志和记录的节点查询（设计 12.3）：日志留在从节点，主节点把后台的查询条件经事件流转给对应节点，结果只显示、不入库。
// 从节点返回的是不可信输入：主节点限制条数、每条长度，要求每条是 JSON 对象，后台一律按纯文本显示。

const (
	// LogQueryTimeout 是每台节点的查询超时（超时的节点在页面上标出）。
	LogQueryTimeout = 10 * time.Second
	// MaxLogRecords、MaxLogBytes：单次查询的条数和大小上限（从节点详情页"最近日志"用更小的 RecentLog*）。
	MaxLogRecords = 200
	MaxLogBytes   = 1 << 20
	// maxLogRecordBytes 是单条记录的长度上限，超过的丢掉并计数。
	maxLogRecordBytes = 64 << 10
	RecentLogRecords  = 200
	RecentLogBytes    = 256 << 10
)

// 节点查询结果的状态。
const (
	LogStatusOK      = "ok"
	LogStatusOffline = "offline"
	LogStatusTimeout = "timeout"
	LogStatusError   = "error"
)

// ErrLogQueryNodeUnavailable：这台节点现在没有事件连接，查不了。
var ErrLogQueryNodeUnavailable = errors.New("relay node is not connected")

// NodeLogResult 是一台节点的查询结果。
type NodeLogResult struct {
	NodeID      int64             `json:"node_id"`
	NodeName    string            `json:"node_name"`
	Status      string            `json:"status"`
	Error       string            `json:"error,omitempty"`
	Truncated   bool              `json:"truncated,omitempty"`
	ScanLimited bool              `json:"scan_limited,omitempty"`
	Skipped     int               `json:"skipped,omitempty"`
	Records     []json.RawMessage `json:"records"`
}

// LogQuerier 把查询转给节点并等结果。
type LogQuerier struct {
	events *EventHub

	mu      sync.Mutex
	pending map[string]chan *relayv1.LogQueryResult
}

// NewLogQuerier 创建并接上事件中心的结果回调。
func NewLogQuerier(events *EventHub) *LogQuerier {
	q := &LogQuerier{events: events, pending: map[string]chan *relayv1.LogQueryResult{}}
	events.OnLogResult = q.deliver
	return q
}

func (q *LogQuerier) deliver(_ int64, r *relayv1.LogQueryResult) {
	q.mu.Lock()
	ch := q.pending[r.GetQueryId()]
	q.mu.Unlock()
	if ch == nil {
		return // 超时之后才到的结果，或不是这里发的查询
	}
	select {
	case ch <- r:
	default:
	}
}

// Query 向一台节点发一次查询并等结果。节点没有事件连接时返回 ErrLogQueryNodeUnavailable；超时返回 context 错误。
func (q *LogQuerier) Query(ctx context.Context, nodeID int64, query *relayv1.LogQuery) (*NodeLogResult, error) {
	if !q.events.IsConnected(nodeID) {
		return nil, ErrLogQueryNodeUnavailable
	}
	id := make([]byte, 12)
	_, _ = rand.Read(id)
	query.QueryId = hex.EncodeToString(id)
	ch := make(chan *relayv1.LogQueryResult, 1)
	q.mu.Lock()
	q.pending[query.QueryId] = ch
	q.mu.Unlock()
	defer func() {
		q.mu.Lock()
		delete(q.pending, query.QueryId)
		q.mu.Unlock()
	}()

	q.events.SendTo(nodeID, &relayv1.MasterEnvelope{Body: &relayv1.MasterEnvelope_LogQuery{LogQuery: query}})
	select {
	case r := <-ch:
		return sanitizeLogResult(nodeID, query, r), nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// sanitizeLogResult 按不可信输入处理从节点的结果：条数、每条长度、总大小上限，每条必须是 JSON 对象。
func sanitizeLogResult(nodeID int64, query *relayv1.LogQuery, r *relayv1.LogQueryResult) *NodeLogResult {
	out := &NodeLogResult{NodeID: nodeID, Status: LogStatusOK, Truncated: r.GetTruncated(), ScanLimited: r.GetScanLimited(), Records: []json.RawMessage{}}
	if e := r.GetError(); e != "" {
		out.Status = LogStatusError
		if len(e) > 200 {
			e = e[:200]
		}
		out.Error = e
		return out
	}
	limit, maxBytes := int(query.GetLimit()), int(query.GetMaxBytes())
	if limit <= 0 || limit > MaxLogRecords {
		limit = MaxLogRecords
	}
	if maxBytes <= 0 || maxBytes > MaxLogBytes {
		maxBytes = MaxLogBytes
	}
	used := 0
	for _, rec := range r.GetRecords() {
		trimmed := bytes.TrimSpace(rec)
		if len(trimmed) == 0 || trimmed[0] != '{' || len(trimmed) > maxLogRecordBytes || !json.Valid(trimmed) {
			out.Skipped++
			continue
		}
		if len(out.Records) >= limit || used+len(trimmed) > maxBytes {
			out.Truncated = true
			break
		}
		used += len(trimmed)
		out.Records = append(out.Records, json.RawMessage(bytes.Clone(trimmed)))
	}
	return out
}

// MergeLogResults 把多台节点的结果按时间新到旧合并成一个列表（每条带节点），最多 limit 条。
func MergeLogResults(results []NodeLogResult, limit int) []MergedLogRecord {
	var all []MergedLogRecord
	for _, r := range results {
		for _, rec := range r.Records {
			var head struct {
				TS int64 `json:"ts"`
			}
			if json.Unmarshal(rec, &head) != nil {
				// 审核记录用 created_at：解析不出时间的排在最后。
				all = append(all, MergedLogRecord{NodeID: r.NodeID, NodeName: r.NodeName, Record: rec})
				continue
			}
			all = append(all, MergedLogRecord{NodeID: r.NodeID, NodeName: r.NodeName, TS: head.TS, Record: rec})
		}
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].TS > all[j].TS })
	if limit > 0 && len(all) > limit {
		all = all[:limit]
	}
	return all
}

// MergedLogRecord 是合并列表里的一条。
type MergedLogRecord struct {
	NodeID   int64           `json:"node_id"`
	NodeName string          `json:"node_name"`
	TS       int64           `json:"ts,omitempty"`
	Record   json.RawMessage `json:"record"`
}
