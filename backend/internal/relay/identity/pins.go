package identity

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"github.com/Wei-Shaw/sub2api/internal/relay/transport"
)

const fileRootPins = "root_pins.json"

// maxRootPins：主节点同时最多有签发中、旧根、预备三个版本，留些余量；超过的列表视为异常不接收。
const maxRootPins = 8

// RootPins 是从节点信任的主从通信根证书指纹（设计 7.4）。
//
//   - 本机配置的指纹（部署时填的那一个）始终信任，管理员在本机改配置随时生效；
//   - 主节点在配置快照、注册、状态查询、领证和续签的回复里给出全部未停用的根指纹
//     （含预备中的），从节点用它整份替换存下来的列表并落盘：新根提前认得，停用的旧根随之去掉。
//
// 这些回复都来自按当前指纹验证过的主节点，所以接收它们不会扩大信任范围之外的东西。
type RootPins struct {
	path       string
	configured []string

	mu     sync.RWMutex
	stored []string
}

// LoadRootPins 读取 dir 下存过的指纹；configured 是本机配置的指纹（至少一个）。
func LoadRootPins(dir string, configured []string) (*RootPins, error) {
	p := &RootPins{path: filepath.Join(dir, fileRootPins), configured: normalizePins(configured)}
	if len(p.configured) == 0 {
		return nil, errors.New("relay: at least one master root fingerprint must be configured")
	}
	raw, err := os.ReadFile(p.path)
	if errors.Is(err, os.ErrNotExist) {
		return p, nil
	}
	if err != nil {
		return nil, err
	}
	var stored []string
	if err := json.Unmarshal(raw, &stored); err != nil {
		// 文件坏了不影响启动：只用本机配置的指纹，下次收到列表时重写。
		return p, nil
	}
	p.stored = normalizePins(stored)
	return p, nil
}

// Pinned 返回当前信任的全部指纹（本机配置 ∪ 存下来的），交给 transport.ClientTLSOptions。
func (p *RootPins) Pinned() []string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return normalizePins(append(append([]string(nil), p.configured...), p.stored...))
}

// Update 用主节点给的列表替换存下来的指纹。空列表、过长的列表不接收（返回 false）；
// 内容没变时不写盘。
func (p *RootPins) Update(fingerprints []string) (bool, error) {
	next := normalizePins(fingerprints)
	if len(next) == 0 || len(next) > maxRootPins {
		return false, nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if equalPins(p.stored, next) {
		return false, nil
	}
	raw, err := json.Marshal(next)
	if err != nil {
		return false, err
	}
	if err := writeFileAtomic(p.path, raw); err != nil {
		return false, err
	}
	p.stored = next
	return true, nil
}

func normalizePins(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, fp := range in {
		fp = transport.NormalizeFingerprint(fp)
		if !validPin(fp) {
			continue
		}
		if _, ok := seen[fp]; ok {
			continue
		}
		seen[fp] = struct{}{}
		out = append(out, fp)
	}
	sort.Strings(out)
	return out
}

// validPin：根证书指纹是 SHA-256 的十六进制（64 位）。
func validPin(fp string) bool {
	if len(fp) != 64 {
		return false
	}
	_, err := hex.DecodeString(fp)
	return err == nil
}

func equalPins(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
