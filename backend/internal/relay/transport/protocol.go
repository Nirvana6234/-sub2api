package transport

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// 协议版本（设计 7.4）：主节点拒绝过旧的从节点。升级时先升主节点（新旧格式都支持），
// 再逐台升级从节点；只有不兼容的改动才提高 MinSupported。
var (
	ProtocolCurrent      = Version{Major: 1, Minor: 0}
	ProtocolMinSupported = Version{Major: 1, Minor: 0}
)

// Version 是主从协议版本。
type Version struct {
	Major uint32
	Minor uint32
}

func (v Version) String() string { return fmt.Sprintf("%d.%d", v.Major, v.Minor) }

// Less 报告 v 是否比 o 旧。
func (v Version) Less(o Version) bool {
	if v.Major != o.Major {
		return v.Major < o.Major
	}
	return v.Minor < o.Minor
}

func parseVersion(s string) (Version, bool) {
	major, minor, ok := strings.Cut(strings.TrimSpace(s), ".")
	if !ok {
		return Version{}, false
	}
	ma, err1 := strconv.ParseUint(major, 10, 32)
	mi, err2 := strconv.ParseUint(minor, 10, 32)
	if err1 != nil || err2 != nil {
		return Version{}, false
	}
	return Version{Major: uint32(ma), Minor: uint32(mi)}, true
}

// 元数据键。
const (
	mdProtocolVersion = "x-relay-proto"
	mdEpoch           = "x-relay-epoch"
	mdIdempotencyKey  = "x-relay-idem-key"
	mdRetryAfterMs    = "x-relay-retry-after-ms"
	mdReason          = "x-relay-reason"
)

// 失败原因（放在 x-relay-reason 尾部元数据里，客户端据此转成类型化的错误）。
const (
	reasonProtocolTooOld = "PROTOCOL_TOO_OLD"
	reasonEpochMismatch  = "EPOCH_MISMATCH"
	reasonOverloaded     = "OVERLOADED"
)

type idempotencyKeyCtx struct{}

// WithIdempotencyKey 给这次调用带上幂等键（设计 7.4）。会改状态的调用
// （选号、额度申请、收回确认、扣费批次、账号事件、释放）都要带。
// 键在调用方生成一次，超时重发时用同一个；纪元变了就换新键重新发起。
func WithIdempotencyKey(ctx context.Context, key string) context.Context {
	return context.WithValue(ctx, idempotencyKeyCtx{}, key)
}

// IncomingEpoch 返回调用方带来的主节点纪元（服务端处理函数用）。
func IncomingEpoch(ctx context.Context) string {
	md, _ := metadata.FromIncomingContext(ctx)
	return firstMD(md, mdEpoch)
}

// EpochMismatch 返回"主节点已重启"错误（与幂等调用的纪元检查同一个），从节点据此清零重来。
// 动钱的调用不管带不带幂等键都要检查纪元。
func EpochMismatch(ctx context.Context) error {
	return reasonError(ctx, codes.FailedPrecondition, reasonEpochMismatch,
		"relay master restarted; start the call again under the new epoch")
}

func idempotencyKeyFrom(ctx context.Context) string {
	key, _ := ctx.Value(idempotencyKeyCtx{}).(string)
	return key
}

// OverloadedError 是主节点回的"稍后重试"（设计 7.4）。从节点不在本地排队，
// 给客户端返回 503 并带上 Retry-After。
type OverloadedError struct {
	RetryAfter time.Duration
	Message    string
}

func (e *OverloadedError) Error() string {
	return fmt.Sprintf("relay master overloaded, retry after %s: %s", e.RetryAfter, e.Message)
}

var (
	// ErrEpochChanged 表示主节点重启过（纪元变了）。带幂等键的调用不能再用旧键重发，
	// 要按新纪元重新发起（设计 7.4）。
	ErrEpochChanged = errors.New("relay master epoch changed")
	// ErrProtocolTooOld 表示从节点的协议版本低于主节点支持的最低版本，需要升级从节点。
	ErrProtocolTooOld = errors.New("relay protocol version is too old for the master")
)

// reasonError 生成带原因尾部元数据的状态错误。
func reasonError(ctx context.Context, code codes.Code, reason, msg string, extra ...string) error {
	pairs := append([]string{mdReason, reason}, extra...)
	_ = setTrailer(ctx, metadata.Pairs(pairs...))
	return status.Error(code, msg)
}

// translateError 把主节点回的状态错误转成本包的类型化错误。
func translateError(err error, trailer metadata.MD) error {
	if err == nil {
		return nil
	}
	st, ok := status.FromError(err)
	if !ok {
		return err
	}
	reason := firstMD(trailer, mdReason)
	switch {
	case st.Code() == codes.ResourceExhausted && reason == reasonOverloaded:
		retry := time.Second
		if ms, err := strconv.ParseInt(firstMD(trailer, mdRetryAfterMs), 10, 64); err == nil && ms > 0 {
			retry = time.Duration(ms) * time.Millisecond
		}
		return &OverloadedError{RetryAfter: retry, Message: st.Message()}
	case st.Code() == codes.FailedPrecondition && reason == reasonEpochMismatch:
		return fmt.Errorf("%w: %s", ErrEpochChanged, st.Message())
	case st.Code() == codes.FailedPrecondition && reason == reasonProtocolTooOld:
		return fmt.Errorf("%w: %s", ErrProtocolTooOld, st.Message())
	}
	return err
}

func firstMD(md metadata.MD, key string) string {
	if vals := md.Get(key); len(vals) > 0 {
		return vals[0]
	}
	return ""
}
