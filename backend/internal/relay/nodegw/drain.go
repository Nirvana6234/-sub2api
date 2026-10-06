package nodegw

import (
	"sync"
	"time"
)

// drainWatcher 排空最长等待（设计 10.4）：主节点说这台在排空，开始计时；超过最长等待时间（通用配置 drain_max_wait_minutes，默认 30）
// 返回 true，调用方关掉对外服务、断开剩余连接（WebSocket 和实时会话可能一直不结束）。取消排空时计时清零。
type drainWatcher struct {
	mu    sync.Mutex
	since time.Time
	fired bool
}

// observe 报告这一刻是否该强制关闭；只在越过最长等待时返回一次 true。
func (w *drainWatcher) observe(draining bool, now time.Time, maxWait time.Duration) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !draining {
		w.since, w.fired = time.Time{}, false
		return false
	}
	if w.since.IsZero() {
		w.since = now
		return false
	}
	if !w.fired && now.Sub(w.since) >= maxWait {
		w.fired = true
		return true
	}
	return false
}
