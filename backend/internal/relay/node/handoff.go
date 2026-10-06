package node

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/relay/sign"
)

// sealedSectionHandoff 与 master.SealedSectionHandoff 相同。
const sealedSectionHandoff = "handoff"

// HandoffSigner 返回给 HandOff 用的签名函数：用主节点加密下发的标记密钥给"交给主节点转发"的请求签名（设计 10.5）。
// 还没收到密钥时返回空串（请求不带标记；主节点分配比例为 0 时会被拒）。
func (c *ConfigCache) HandoffSigner(nodeID func() int64, now func() time.Time) func(method, path string) string {
	var (
		mu  sync.Mutex
		raw []byte
		key []byte
	)
	return func(method, path string) string {
		sealed, ok := c.SealedSection(sealedSectionHandoff)
		if !ok {
			return ""
		}
		mu.Lock()
		if !bytes.Equal(raw, sealed) {
			var payload struct {
				Key string `json:"key"`
			}
			var k []byte
			if json.Unmarshal(sealed, &payload) == nil {
				if decoded, err := base64.StdEncoding.DecodeString(payload.Key); err == nil {
					k = decoded
				}
			}
			raw, key = append(raw[:0], sealed...), k
		}
		k := key
		mu.Unlock()
		if len(k) == 0 {
			return ""
		}
		return sign.SignHandoff(k, nodeID(), method, path, now())
	}
}
