package master

import (
	"context"
	"crypto/ecdh"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/relay/proto/relayv1"
	"github.com/Wei-Shaw/sub2api/internal/relay/sealbox"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

// SealedSettingKeys 是按节点加密下发的系统设置（设计 6 第二类"转发功能要用的密钥加密下发"）：
// 值里有密钥，但从节点要在本地执行这些功能（分工原则，设计第 1 节）。新增项要在代码评审时确认
// 它确实是转发要用的；与转发无关的密钥（管理员 API Key、SMTP、OAuth 等）一律不进这张表。
var SealedSettingKeys = []string{
	// 内容审核配置（含审核接口 Key），设计 3.4。
	service.SettingKeyContentModerationConfig,
	// 联网搜索配置（含搜索服务 Key），设计 3.3。
	service.SettingKeyWebSearchEmulationConfig,
}

// SealedSectionProxies 是加密下发的代理分段名（JSON：[]service.Proxy，含代理密码）：
// 加密下发的配置引用的代理（审核接口、联网搜索服务）。
const SealedSectionProxies = "proxies"

// SealedPayload 是加密下发部分的明文（只在主节点内存和从节点内存里出现）。
type SealedPayload struct {
	Settings map[string]string `json:"settings,omitempty"`
	Sections map[string][]byte `json:"sections,omitempty"`
}

func (p SealedPayload) empty() bool { return len(p.Settings) == 0 && len(p.Sections) == 0 }

// SealedAAD 把加密下发的内容绑定到节点和快照版本（从节点解开时用同样的附加数据）。
func SealedAAD(nodeID int64, version string) []byte {
	return []byte("sub2api-relay-sealed-config|v1|node:" + strconv.FormatInt(nodeID, 10) + "|" + version)
}

// sealedState 是一次生成的加密下发部分。mac 参与快照版本：内容变了版本就变，但版本不泄露内容
// （密钥是主节点进程内的随机数，重启后换一个，节点随之重新拉取一次）。
type sealedState struct {
	payload SealedPayload
	mac     []byte
}

func buildSealed(ctx context.Context, settings SettingsReader, sections map[string]SectionProvider, macKey []byte) (*sealedState, error) {
	values, err := settings.GetMultiple(ctx, SealedSettingKeys)
	if err != nil {
		return nil, fmt.Errorf("read sealed settings: %w", err)
	}
	p := SealedPayload{Settings: map[string]string{}, Sections: map[string][]byte{}}
	for _, key := range SealedSettingKeys {
		if v, ok := values[key]; ok && v != "" {
			p.Settings[key] = v
		}
	}
	for name, provide := range sections {
		raw, err := provide(ctx)
		if err != nil {
			return nil, fmt.Errorf("build sealed section %s: %w", name, err)
		}
		if len(raw) > 0 {
			p.Sections[name] = raw
		}
	}
	mac := hmac.New(sha256.New, macKey)
	_, _ = mac.Write(canonicalMap(p.Settings))
	_, _ = mac.Write(canonicalBytesMap(p.Sections))
	return &sealedState{payload: p, mac: mac.Sum(nil)}, nil
}

func newSealedMACKey() []byte {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		panic(fmt.Errorf("relay: random sealed config key: %w", err))
	}
	return key
}

// sealFor 按节点的加密公钥封好加密下发部分，放进快照。没有要下发的内容时不动快照。
// 节点加密公钥参与版本：节点续签换了加密密钥，版本随之变化，从节点按新版本重新拉取。
func (g *globalSnapshot) sealFor(snap *relayv1.ConfigSnapshot, nodeID int64, pub *ecdh.PublicKey) error {
	if g.sealed == nil || g.sealed.payload.empty() {
		return nil
	}
	if pub == nil {
		// 已领证的节点一定有加密公钥（领证、续签时上报，主节点重启从库里恢复）。拿不到就报错：宁可让这台
		// 拉不到配置（请求 503），也不能让它悄悄少了审核配置、把该拦的请求放过去。
		return fmt.Errorf("relay node %d has no encryption key; sealed config cannot be delivered", nodeID)
	}
	snap.Version = hashParts([]byte(snap.Version), pub.Bytes())
	plain, err := json.Marshal(g.sealed.payload)
	if err != nil {
		return err
	}
	sealed, err := sealbox.Seal(pub, plain, SealedAAD(nodeID, snap.Version))
	if err != nil {
		return fmt.Errorf("seal relay config for node %d: %w", nodeID, err)
	}
	snap.Sealed = sealed
	return nil
}
