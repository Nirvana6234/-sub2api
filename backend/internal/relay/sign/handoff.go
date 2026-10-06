package sign

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
	"time"
)

// 交给主节点转发的请求的身份标记（设计 10.5）：从节点把自己转发不了的请求（实时会话、Grok OAuth、组合平台媒体等）原样交给主节点，
// 主节点分配比例为 0 时主节点不接客户端直接打来的 API Key 请求，但必须接这些来自从节点的。
// 主节点用一把进程内随机密钥（加密下发给各从节点）对"节点 ID、时间、方法、路径"做 HMAC，请求头带上它；
// 主节点验过才算来自从节点。客户端拿不到密钥，也不能把别人的标记用到别的路径上。

// HandoffHeader 是请求头名。
const HandoffHeader = "X-Sub2api-Relay-Handoff"

// HandoffValidity 是标记的有效期（两侧时钟允许的偏差一起算在内）。
const HandoffValidity = 5 * time.Minute

// ErrBadHandoff：标记缺失、格式不对、过期或签名不符。
var ErrBadHandoff = errors.New("invalid relay hand-off marker")

func handoffMAC(key []byte, nodeID int64, ts int64, method, path string) string {
	m := hmac.New(sha256.New, key)
	_, _ = m.Write([]byte("sub2api-relay-handoff|v1|" + strconv.FormatInt(nodeID, 10) + "|" + strconv.FormatInt(ts, 10) + "|" + strings.ToUpper(method) + "|" + path))
	return hex.EncodeToString(m.Sum(nil))
}

// SignHandoff 生成请求头的值：v1.<节点 ID>.<时间戳>.<HMAC>。
func SignHandoff(key []byte, nodeID int64, method, path string, now time.Time) string {
	ts := now.Unix()
	return "v1." + strconv.FormatInt(nodeID, 10) + "." + strconv.FormatInt(ts, 10) + "." + handoffMAC(key, nodeID, ts, method, path)
}

// VerifyHandoff 验请求头的值，返回签发它的节点 ID。
func VerifyHandoff(key []byte, header, method, path string, now time.Time) (int64, error) {
	parts := strings.Split(header, ".")
	if len(key) == 0 || len(parts) != 4 || parts[0] != "v1" {
		return 0, ErrBadHandoff
	}
	nodeID, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || nodeID <= 0 {
		return 0, ErrBadHandoff
	}
	ts, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil {
		return 0, ErrBadHandoff
	}
	age := now.Sub(time.Unix(ts, 0))
	if age < -HandoffValidity || age > HandoffValidity {
		return 0, ErrBadHandoff
	}
	want := handoffMAC(key, nodeID, ts, method, path)
	if !hmac.Equal([]byte(want), []byte(parts[3])) {
		return 0, ErrBadHandoff
	}
	return nodeID, nil
}
