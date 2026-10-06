package nodegw

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/relay/node"
	"github.com/Wei-Shaw/sub2api/internal/relay/nodestore"
	"github.com/Wei-Shaw/sub2api/internal/relay/proto/relayv1"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/Wei-Shaw/sub2api/internal/util/logredact"
)

// 从节点本机的日志和记录（设计第 12 节）：程序日志、请求错误日志、审核记录都写进本机数据目录（nodestore），
// 由主节点把后台的查询转过来执行；请求成功时主从之间没有任何日志流量，主节点只在心跳里收到按（级别, 组件）、
// （错误类型, 状态码）汇总的条数。

// 本机存储里的种类。
const (
	logKindApp   = "app"
	logKindError = "error"
)

// 查询上限（设计 12.3）：单次条数和大小；扫描量超过上限没扫完时回 scan_limited。
const (
	maxLogQueryRecords = 200
	maxLogQueryBytes   = 1 << 20
	maxLogScanLines    = 2_000_000
)

// 从节点本机日志的脱敏：在单机的 logredact 规则（令牌、密码、授权码等键）之上，再去掉 API Key 和 Bearer 令牌——
// 这台机器直接面对公网和第三方 Key，日志里不留这些。
var (
	redactExtraKeys = []string{"api_key", "apikey", "authorization", "x-api-key", "key", "token", "secret", "upstream_key", "cookie"}
	reBearerToken   = regexp.MustCompile(`(?i)(bearer\s+)[A-Za-z0-9._~+/=-]{6,}`)
	reAPIKeyLike    = regexp.MustCompile(`\b(sk|rk|pk)-[A-Za-z0-9_-]{6,}`)
)

func redactText(s string) string {
	s = logredact.RedactText(s, redactExtraKeys...)
	s = reBearerToken.ReplaceAllString(s, "${1}***")
	return reAPIKeyLike.ReplaceAllString(s, "${1}-***")
}

// redactFields 脱敏一个字段表：按键名脱敏后，再对整个 JSON 里的字符串做令牌模式替换。
func redactFields(m map[string]any) map[string]any {
	out := logredact.RedactMap(m, redactExtraKeys...)
	raw, err := json.Marshal(out)
	if err != nil {
		return out
	}
	cleaned := reAPIKeyLike.ReplaceAll(reBearerToken.ReplaceAll(raw, []byte("${1}***")), []byte("${1}-***"))
	var back map[string]any
	if json.Unmarshal(cleaned, &back) != nil {
		return out
	}
	return back
}

// logRecord 是 app 和 error 两种记录共有的顶层字段（过滤只看这些和关键字）。
type logRecord struct {
	TS        int64          `json:"ts"`
	Level     string         `json:"level,omitempty"`
	Component string         `json:"component,omitempty"`
	Message   string         `json:"message,omitempty"`
	RequestID string         `json:"request_id,omitempty"`
	UserID    int64          `json:"user_id,omitempty"`
	APIKeyID  int64          `json:"api_key_id,omitempty"`
	AccountID int64          `json:"account_id,omitempty"`
	Platform  string         `json:"platform,omitempty"`
	Model     string         `json:"model,omitempty"`
	Fields    map[string]any `json:"fields,omitempty"`
}

// LogSink 实现 logger.Sink：把程序日志写进本机存储（与单机写数据库的那部分一致：警告以上和审计；
// 访问日志只在打开时才有），脱敏规则与单机相同（logredact）。写入在后台协程里做，队列满了丢弃并计数。
type LogSink struct {
	store *nodestore.Store
	stats *Stats
	queue chan *logger.LogEvent
	wg    sync.WaitGroup
	done  chan struct{}

	dropped atomic.Int64
	// PersistAccessLogs 为 true 时访问日志（http.access）也写。
	persistAccess atomic.Bool
}

// NewLogSink 创建并启动日志写入；Close 时把队列里剩下的写完。
func NewLogSink(store *nodestore.Store, stats *Stats) *LogSink {
	s := &LogSink{store: store, stats: stats, queue: make(chan *logger.LogEvent, 5000), done: make(chan struct{})}
	s.wg.Add(1)
	go s.run()
	return s
}

// SetPersistAccessLogs 打开或关闭访问日志的落盘（高流量，默认关）。
func (s *LogSink) SetPersistAccessLogs(on bool) { s.persistAccess.Store(on) }

// Dropped 返回因队列满而丢弃的条数（心跳里报）。
func (s *LogSink) Dropped() int64 { return s.dropped.Load() }

// Backlog 返回还没写进本机存储的条数。
func (s *LogSink) Backlog() int { return len(s.queue) }

// WriteLogEvent 实现 logger.Sink。
func (s *LogSink) WriteLogEvent(e *logger.LogEvent) {
	if e == nil || !s.shouldStore(e) {
		return
	}
	if s.stats != nil {
		s.stats.NoteLog(strings.ToLower(e.Level), componentOf(e))
	}
	select {
	case s.queue <- e:
	default:
		s.dropped.Add(1)
	}
}

func componentOf(e *logger.LogEvent) string {
	if e.Fields != nil {
		if c, _ := e.Fields["component"].(string); strings.TrimSpace(c) != "" {
			return strings.TrimSpace(c)
		}
	}
	return strings.TrimSpace(e.Component)
}

func (s *LogSink) shouldStore(e *logger.LogEvent) bool {
	if e.Fields != nil {
		if skip, _ := e.Fields[logger.OpsSystemLogSkipField].(bool); skip {
			return false
		}
	}
	switch strings.ToLower(strings.TrimSpace(e.Level)) {
	case "warn", "warning", "error", "fatal", "panic", "dpanic":
		return true
	}
	component := strings.ToLower(componentOf(e))
	if strings.Contains(component, "http.access") {
		return s.persistAccess.Load()
	}
	return strings.Contains(component, "audit")
}

func (s *LogSink) run() {
	defer s.wg.Done()
	for {
		select {
		case e := <-s.queue:
			s.write(e)
		case <-s.done:
			for {
				select {
				case e := <-s.queue:
					s.write(e)
				default:
					return
				}
			}
		}
	}
}

func (s *LogSink) write(e *logger.LogEvent) {
	rec := logRecord{TS: e.Time.UnixMilli(), Level: strings.ToLower(e.Level), Component: componentOf(e), Message: redactText(e.Message)}
	if rec.TS <= 0 {
		rec.TS = time.Now().UnixMilli()
	}
	fields := redactFields(e.Fields)
	rec.RequestID = firstString(fields, "request_id", "client_request_id")
	rec.UserID = firstInt(fields, "user_id")
	rec.APIKeyID = firstInt(fields, "api_key_id")
	rec.AccountID = firstInt(fields, "account_id")
	rec.Platform = firstString(fields, "platform")
	rec.Model = firstString(fields, "model")
	delete(fields, "component")
	if len(fields) > 0 {
		rec.Fields = fields
	}
	if err := s.store.Append(logKindApp, rec); err != nil && err != nodestore.ErrClosed {
		// 日志写不进去不能再记成日志（会循环），只在标准库日志里留一行。
		slog.Default().With(logger.OpsSystemLogSkipField, true).Warn("relay node log could not be stored", "error", err)
	}
}

// Close 写完队列里剩下的。
func (s *LogSink) Close() {
	close(s.done)
	s.wg.Wait()
}

func firstString(m map[string]any, keys ...string) string {
	for _, k := range keys {
		switch v := m[k].(type) {
		case string:
			if v != "" {
				return v
			}
		}
	}
	return ""
}

func firstInt(m map[string]any, key string) int64 {
	switch v := m[key].(type) {
	case int64:
		return v
	case int:
		return int64(v)
	case float64:
		return int64(v)
	case string:
		n, _ := strconv.ParseInt(v, 10, 64)
		return n
	case json.Number:
		n, _ := v.Int64()
		return n
	}
	return 0
}

// NodeOpsRepository 是从节点上的运维仓储：只实现请求错误日志的写入（service.OpsService.RecordError 要用的），写进本机存储；
// 其余查询一律没有（后台经主节点转来查本机记录，不走 OpsService）。
type NodeOpsRepository struct {
	service.OpsRepository
	store *nodestore.Store
	stats *Stats
}

// NewNodeOpsRepository 创建。
func NewNodeOpsRepository(store *nodestore.Store, stats *Stats) *NodeOpsRepository {
	return &NodeOpsRepository{store: store, stats: stats}
}

func (r *NodeOpsRepository) InsertErrorLog(_ context.Context, in *service.OpsInsertErrorLogInput) (int64, error) {
	if in == nil {
		return 0, nil
	}
	raw, err := json.Marshal(in)
	if err != nil {
		return 0, err
	}
	var entry map[string]any
	if err := json.Unmarshal(raw, &entry); err != nil {
		return 0, err
	}
	rec := map[string]any{"ts": in.CreatedAt.UnixMilli(), "level": in.Severity, "component": in.ErrorPhase, "message": redactText(in.ErrorMessage),
		"request_id": in.RequestID, "platform": in.Platform, "model": in.Model, "status_code": in.StatusCode, "error_type": in.ErrorType,
		"entry": redactFields(entry)}
	if in.UserID != nil {
		rec["user_id"] = *in.UserID
	}
	if in.APIKeyID != nil {
		rec["api_key_id"] = *in.APIKeyID
	}
	if in.AccountID != nil {
		rec["account_id"] = *in.AccountID
	}
	if in.CreatedAt.IsZero() {
		rec["ts"] = time.Now().UnixMilli()
	}
	if r.stats != nil {
		r.stats.NoteError(in.ErrorType, strconv.Itoa(in.StatusCode))
	}
	return 0, r.store.Append(logKindError, rec)
}

func (r *NodeOpsRepository) BatchInsertErrorLogs(ctx context.Context, in []*service.OpsInsertErrorLogInput) (int64, error) {
	var n int64
	for _, e := range in {
		if _, err := r.InsertErrorLog(ctx, e); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

// LogQueryExecutor 执行主节点转来的日志和记录查询（设计 12.3）：只读本机存储，结果有条数和大小上限。
type LogQueryExecutor struct {
	store      *nodestore.Store
	moderation *node.ModerationStore
}

// NewLogQueryExecutor 创建。moderation 为 nil 时不能查审核记录。
func NewLogQueryExecutor(store *nodestore.Store, moderation *node.ModerationStore) *LogQueryExecutor {
	return &LogQueryExecutor{store: store, moderation: moderation}
}

// Execute 执行一次查询；错误写在结果里（主节点只显示，不当可信输入）。
func (e *LogQueryExecutor) Execute(ctx context.Context, q *relayv1.LogQuery) *relayv1.LogQueryResult {
	res := &relayv1.LogQueryResult{QueryId: q.GetQueryId()}
	limit := int(q.GetLimit())
	if limit <= 0 || limit > maxLogQueryRecords {
		limit = maxLogQueryRecords
	}
	maxBytes := int(q.GetMaxBytes())
	if maxBytes <= 0 || maxBytes > maxLogQueryBytes {
		maxBytes = maxLogQueryBytes
	}
	switch q.GetKind() {
	case logKindApp, logKindError:
		e.scanKind(q, limit, maxBytes, res)
	case "moderation":
		e.moderationRecords(ctx, q, limit, maxBytes, res)
	default:
		res.Error = "unsupported log kind"
	}
	return res
}

func (e *LogQueryExecutor) scanKind(q *relayv1.LogQuery, limit, maxBytes int, res *relayv1.LogQueryResult) {
	var from, to time.Time
	if q.GetFromUnixMs() > 0 {
		from = time.UnixMilli(q.GetFromUnixMs())
	}
	if q.GetToUnixMs() > 0 {
		to = time.UnixMilli(q.GetToUnixMs())
	}
	keyword := strings.ToLower(strings.TrimSpace(q.GetKeyword()))
	used, scanned := 0, 0
	err := e.store.Scan(q.GetKind(), from, to, func(line []byte) bool {
		scanned++
		if scanned > maxLogScanLines {
			res.ScanLimited = true
			return false
		}
		var rec logRecord
		if json.Unmarshal(line, &rec) != nil || !logMatches(&rec, q, line, keyword) {
			return true
		}
		if len(res.Records) >= limit || used+len(line) > maxBytes {
			res.Truncated = true
			return false
		}
		used += len(line)
		res.Records = append(res.Records, bytes.Clone(line))
		return true
	})
	if err != nil {
		res.Error = "log store could not be read"
		slog.Warn("relay node log query failed", "kind", q.GetKind(), "error", err)
	}
}

func logMatches(r *logRecord, q *relayv1.LogQuery, line []byte, keyword string) bool {
	if q.GetFromUnixMs() > 0 && r.TS < q.GetFromUnixMs() {
		return false
	}
	if q.GetToUnixMs() > 0 && r.TS > q.GetToUnixMs() {
		return false
	}
	if q.GetBeforeUnixMs() > 0 && r.TS >= q.GetBeforeUnixMs() {
		return false
	}
	if v := q.GetLevel(); v != "" && !strings.EqualFold(r.Level, v) {
		return false
	}
	if v := q.GetComponent(); v != "" && !strings.Contains(strings.ToLower(r.Component), strings.ToLower(v)) {
		return false
	}
	if v := q.GetRequestId(); v != "" && r.RequestID != v {
		return false
	}
	if v := q.GetUserId(); v != 0 && r.UserID != v {
		return false
	}
	if v := q.GetApiKeyId(); v != 0 && r.APIKeyID != v {
		return false
	}
	if v := q.GetAccountId(); v != 0 && r.AccountID != v {
		return false
	}
	if v := q.GetPlatform(); v != "" && !strings.EqualFold(r.Platform, v) {
		return false
	}
	if v := q.GetModel(); v != "" && !strings.EqualFold(r.Model, v) {
		return false
	}
	if keyword != "" && !strings.Contains(strings.ToLower(string(line)), keyword) {
		return false
	}
	return true
}

func (e *LogQueryExecutor) moderationRecords(ctx context.Context, q *relayv1.LogQuery, limit, maxBytes int, res *relayv1.LogQueryResult) {
	if e.moderation == nil {
		res.Error = "moderation records are not available"
		return
	}
	filter := service.ContentModerationLogFilter{
		Pagination: pagination.PaginationParams{Page: int(q.GetPage()), PageSize: limit},
		Result:     q.GetResult(), Search: q.GetKeyword(), Endpoint: q.GetComponent(),
	}
	if q.GetFromUnixMs() > 0 {
		t := time.UnixMilli(q.GetFromUnixMs())
		filter.From = &t
	}
	if q.GetToUnixMs() > 0 {
		t := time.UnixMilli(q.GetToUnixMs())
		filter.To = &t
	}
	logs, page, err := e.moderation.ListLogs(ctx, filter)
	if err != nil {
		res.Error = "moderation records could not be read"
		return
	}
	used := 0
	for _, l := range logs {
		line, err := json.Marshal(l)
		if err != nil {
			continue
		}
		if used+len(line) > maxBytes {
			res.Truncated = true
			break
		}
		used += len(line)
		res.Records = append(res.Records, line)
	}
	if page != nil && page.Total > int64(page.Page*page.PageSize) {
		res.Truncated = true
	}
}
