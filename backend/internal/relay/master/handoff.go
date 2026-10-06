package master

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/relay/sign"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

// SealedSectionHandoff 是加密下发的"交给主节点转发"的标记密钥（JSON：{"key": base64}）。密钥是主节点进程内的随机数，
// 重启后换一个，节点随配置版本变化重新拉取（设计 10.5，sign/handoff.go）。
const SealedSectionHandoff = "handoff"

func newHandoffKey() ([]byte, error) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	return key, nil
}

func handoffSection(key []byte) SectionProvider {
	raw, _ := json.Marshal(map[string]string{"key": base64.StdEncoding.EncodeToString(key)})
	return func(context.Context) ([]byte, error) { return raw, nil }
}

var _ middleware.RelayMasterGate = (*Runtime)(nil)

// BlockMasterForwarding 实现 middleware.RelayMasterGate：主从分流在运行且主节点分配比例为 0 时，主节点不转发（设计 10.5）。
func (r *Runtime) BlockMasterForwarding(ctx context.Context) bool {
	if _, err := r.runningRelay(); err != nil {
		return false
	}
	return *r.cachedGeneralConfig(ctx).WithDefaults().MasterRatioPercent == 0
}

// VerifyHandoff 实现 middleware.RelayMasterGate：标记要是这次运行的密钥签的、签发的节点还在服务（激活或排空）。
func (r *Runtime) VerifyHandoff(header, method, path string) bool {
	rr, err := r.runningRelay()
	if err != nil || len(rr.handoffKey) == 0 {
		return false
	}
	nodeID, err := sign.VerifyHandoff(rr.handoffKey, header, method, path, r.now())
	if err != nil {
		return false
	}
	return rr.nodes != nil && rr.nodes.Status(nodeID).Serving()
}

// AssignedAddress 实现 middleware.RelayMasterGate：这把 Key 分配的地址（拒绝提示用）。
func (r *Runtime) AssignedAddress(ctx context.Context, rawKey string) string {
	rawKey = strings.TrimSpace(rawKey)
	if rawKey == "" || r.deps.APIKeys == nil {
		return ""
	}
	key, err := r.deps.APIKeys.GetByKey(ctx, rawKey)
	if err != nil || key == nil {
		return ""
	}
	if addr, ok := service.ResolveRelayAddressForKey(ctx, key.RelayNodeID); ok {
		return addr.BaseURL
	}
	return ""
}
