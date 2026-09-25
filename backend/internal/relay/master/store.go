// Package master 是主从分流的主节点侧（docs/MASTER_RELAY_NODES.md）。
//
// WP3 起包含：节点注册与激活、主从通信证书的签发 / 续签 / 恢复 / 吊销、
// "同一身份出现两份"的检测，以及主节点私钥（keystore）的使用。
// 持久化通过 NodeStore 接口，实现在 internal/repository（原始 SQL）。
package master

import (
	"context"
	"errors"
	"time"
)

// NodeStatus 是节点状态，与 relay_nodes.status 一致。
type NodeStatus string

const (
	NodePending  NodeStatus = "pending"
	NodeActive   NodeStatus = "active"
	NodeDraining NodeStatus = "draining"
	NodeDisabled NodeStatus = "disabled"
	NodeRejected NodeStatus = "rejected"
)

// Serving 报告这个状态下节点能否用签发证书通信：排空中的节点还要把进行中的请求跑完、
// 把扣费队列发完、退回额度（设计 10.4），所以和已激活一样可以通信，只是不再分配新用户。
func (s NodeStatus) Serving() bool { return s == NodeActive || s == NodeDraining }

// Node 是一台从节点。
type Node struct {
	ID                  int64
	Name                string
	Hostname            string
	Region              string
	PublicDomain        string
	Status              NodeStatus
	IdentityPublicKey   []byte // 长期密钥公钥，PKIX DER
	IdentityFingerprint string
	EncryptionPublicKey []byte // X25519，32 字节
	RegisteredIP        string
	ProgramVersion      string
	SystemInfo          map[string]string
	BandwidthLimitMbps  int
	AllowMultiIP        bool
	ActivatedAt         *time.Time
	ActivatedBy         *int64
	LastSeenAt          *time.Time
	LastSeenIP          string
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

// Certificate 是签发给节点的一张主从通信证书。
type Certificate struct {
	ID                int64
	NodeID            int64
	Serial            string
	PublicKey         []byte // PKIX DER
	NotBefore         time.Time
	NotAfter          time.Time
	RenewedFromSerial string
	RevokedAt         *time.Time
	RevokeReason      string
	CreatedAt         time.Time
}

// Activation 是管理员激活节点时填写的内容（设计 11.1 第 5 步）。
type Activation struct {
	Name               string
	PublicDomain       string
	BandwidthLimitMbps int
	Region             string
	ActorUserID        int64
	At                 time.Time
}

// AuditEntry 是一条节点审计记录。
type AuditEntry struct {
	NodeID      int64 // 0 表示与具体节点无关（如"一键拒绝所有待激活"）
	ActorUserID int64 // 0 表示系统动作
	Action      string
	SourceIP    string
	Detail      map[string]any
}

var (
	ErrNodeNotFound = errors.New("relay node not found")
	// ErrPendingLimit：待激活节点已达上限，拒绝新注册（设计 11.1 防刷）。
	ErrPendingLimit = errors.New("too many relay nodes are waiting for activation")
	// ErrDuplicateRenewal：同一张证书已经被续签过一次（设计 7.2 第 2 条）。
	ErrDuplicateRenewal = errors.New("relay certificate was already renewed")
	// ErrStatusConflict：节点当前状态不允许这个操作。
	ErrStatusConflict = errors.New("relay node status does not allow this operation")
	// ErrDomainTaken：对外域名已被别的节点使用。
	ErrDomainTaken = errors.New("relay node public domain is already in use")
)

// NodeStore 是节点、证书、审计的持久化。实现必须保证：
//   - CreatePending 在同一事务里检查待激活数量上限；
//   - InsertCertificate 在 renewed_from_serial 重复时返回 ErrDuplicateRenewal；
//   - SetStatus 只在当前状态属于 from 时修改（条件更新），返回是否改了。
type NodeStore interface {
	CreatePending(ctx context.Context, n *Node, maxPending int) (*Node, error)
	UpdateRegistration(ctx context.Context, id int64, hostname, programVersion, displayName string, systemInfo map[string]string, ip string) error
	GetByID(ctx context.Context, id int64) (*Node, error)
	GetByFingerprint(ctx context.Context, fingerprint string) (*Node, error)
	List(ctx context.Context) ([]*Node, error)
	SetStatus(ctx context.Context, id int64, from []NodeStatus, to NodeStatus) (bool, error)
	Activate(ctx context.Context, id int64, a Activation) error
	SetAllowMultiIP(ctx context.Context, id int64, allow bool) error
	SetEncryptionKey(ctx context.Context, id int64, key []byte) error
	// TouchSeen 记录最近一次看到节点的时间和 IP，返回 IP 是否和上次不同。
	TouchSeen(ctx context.Context, id int64, ip string, at time.Time) (ipChanged bool, err error)
	PurgeStalePending(ctx context.Context, before time.Time) (int64, error)
	InsertCertificate(ctx context.Context, c *Certificate) error
	GetCertificate(ctx context.Context, serial string) (*Certificate, error)
	CountCertificates(ctx context.Context, nodeID int64) (int, error)
	// RevokeCertificates 吊销节点所有未吊销、未过期的证书，返回被吊销的序列号。
	RevokeCertificates(ctx context.Context, nodeID int64, reason string, at time.Time) ([]string, error)
	// ListRevokedSerials 返回 notAfter 之后才过期的已吊销证书（过期的证书握手本来就过不了）。
	ListRevokedSerials(ctx context.Context, notAfter time.Time) ([]string, error)
	Audit(ctx context.Context, e AuditEntry) error
}
