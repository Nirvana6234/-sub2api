package transport

import (
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// CallClass 是按对端限流的调用类别（设计 7.4：每台的额度申请、选号、上游错误决策、
// 联网搜索、内容审核、账号事件、扣费批次、Key 查询、查询接口转交、日志都有速率上限）。
type CallClass string

const (
	ClassEnrollment CallClass = "enrollment"
	ClassControl    CallClass = "control"
	ClassEvents     CallClass = "events"
	ClassBilling    CallClass = "billing"
	ClassModeration CallClass = "moderation"
	ClassLogs       CallClass = "logs"
)

// Limit 是一类调用对单个对端的上限。零值字段表示不限。
type Limit struct {
	// RatePerSecond / Burst：令牌桶。
	RatePerSecond float64
	Burst         int
	// MaxInflight：同时在执行的调用数。
	MaxInflight int
	// RetryAfter：超限时建议从节点等多久（默认 1 秒）。
	RetryAfter time.Duration
}

// limiter 按（对端, 类别）限流。超限时立即回"稍后重试"，不排队（设计 7.4）。
type limiter struct {
	limits map[CallClass]Limit
	mu     sync.Mutex
	states map[string]*limiterState
	now    func() time.Time
}

type limiterState struct {
	bucket   *rate.Limiter
	inflight int
	lastUsed time.Time
}

func newLimiter(limits map[CallClass]Limit) *limiter {
	return &limiter{limits: limits, states: make(map[string]*limiterState), now: time.Now}
}

// acquire 返回释放函数；超限时返回 ok=false 和建议等待时间。
func (l *limiter) acquire(peerKey string, class CallClass) (release func(), retryAfter time.Duration, ok bool) {
	limit, configured := l.limits[class]
	if !configured {
		return func() {}, 0, true
	}
	retry := limit.RetryAfter
	if retry <= 0 {
		retry = time.Second
	}
	key := string(class) + "|" + peerKey
	now := l.now()

	l.mu.Lock()
	st := l.states[key]
	if st == nil {
		st = &limiterState{}
		if limit.RatePerSecond > 0 {
			burst := limit.Burst
			if burst <= 0 {
				burst = 1
			}
			st.bucket = rate.NewLimiter(rate.Limit(limit.RatePerSecond), burst)
		}
		l.states[key] = st
	}
	st.lastUsed = now
	if limit.MaxInflight > 0 && st.inflight >= limit.MaxInflight {
		l.mu.Unlock()
		return nil, retry, false
	}
	if st.bucket != nil {
		r := st.bucket.ReserveN(now, 1)
		if !r.OK() {
			l.mu.Unlock()
			return nil, retry, false
		}
		if delay := r.DelayFrom(now); delay > 0 {
			r.CancelAt(now)
			l.mu.Unlock()
			if delay > retry {
				retry = delay
			}
			return nil, retry, false
		}
	}
	st.inflight++
	l.sweepLocked(now)
	l.mu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			l.mu.Lock()
			st.inflight--
			l.mu.Unlock()
		})
	}, 0, true
}

// sweepLocked 清掉 10 分钟没用过、也没有在执行调用的对端状态，匿名来源 IP 不会无限累积。
func (l *limiter) sweepLocked(now time.Time) {
	if len(l.states) < 1024 {
		return
	}
	for k, st := range l.states {
		if st.inflight == 0 && now.Sub(st.lastUsed) > 10*time.Minute {
			delete(l.states, k)
		}
	}
}
