// Package master 是主从分流的主节点侧（docs/MASTER_RELAY_NODES.md）。
//
// WP3 起包含：节点注册与激活、主从通信证书的签发 / 续签 / 恢复 / 吊销、
// "同一身份出现两份"的检测，以及主节点私钥（keystore）的使用。
// 持久化通过 NodeStore 接口，实现在 internal/repository（原始 SQL）。
package master

import (
	"context"
	"errors"
	"strings"
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
	DER               []byte // 签发的证书原文，续签重放时原样返回
	NotBefore         time.Time
	NotAfter          time.Time
	RenewedFromSerial string
	RevokedAt         *time.Time
	RevokeReason      string
	CreatedAt         time.Time
}

// IsSuspectRevokeReason 报告一个吊销原因是否表示怀疑节点被攻破（身份重复、管理员吊销）。
func IsSuspectRevokeReason(reason string) bool {
	return reason == revokeReasonIdentityDuplicated || strings.HasPrefix(reason, revokeReasonAdminPrefix)
}

const (
	revokeReasonIdentityDuplicated = "identity_duplicated"
	revokeReasonAdminPrefix        = "revoked:"
)

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
	// ErrFingerprintMismatch：激活时核对的指纹和节点注册时的不一致。
	ErrFingerprintMismatch = errors.New("the fingerprint does not match the one this node registered with")
	// ErrDomainRequired：激活节点必须填对外域名。
	ErrDomainRequired = errors.New("a public domain is required to activate a relay node")
	// ErrInvalidDomain：对外地址必须是主机名或 IP，可带端口，不能带协议或路径。
	ErrInvalidDomain = errors.New("the public relay address is invalid")
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
	UpdatePublicDomain(ctx context.Context, id int64, domain string) error
	// ReplaceNode 换机器（设计 11.6）：把已停用的 from 节点的对外域名转给待激活的 to 节点并激活 to，
	// 同一个事务里清掉 from 的域名（域名全局唯一）。from 不是停用状态、to 不是待激活时返回 ErrStatusConflict。
	ReplaceNode(ctx context.Context, fromID, toID int64, a Activation) error
	SetAllowMultiIP(ctx context.Context, id int64, allow bool) error
	SetEncryptionKey(ctx context.Context, id int64, key []byte) error
	// TouchSeen 记录最近一次看到节点的时间和 IP，返回 IP 是否和上次不同。
	TouchSeen(ctx context.Context, id int64, ip string, at time.Time) (ipChanged bool, err error)
	PurgeStalePending(ctx context.Context, before time.Time) (int64, error)
	InsertCertificate(ctx context.Context, c *Certificate) error
	GetCertificate(ctx context.Context, serial string) (*Certificate, error)
	// GetRenewalOf 返回由 serial 续签出的那张证书；没有时返回 ErrNodeNotFound。
	GetRenewalOf(ctx context.Context, serial string) (*Certificate, error)
	// ListRenewedSerials 返回 notAfter 之后才过期、且已经被续签替换的证书：
	// 它们不能再建新连接（设计 7.2 第 1 条），主节点重启后要从库里恢复这份名单。
	ListRenewedSerials(ctx context.Context, notAfter time.Time) ([]string, error)
	CountCertificates(ctx context.Context, nodeID int64) (int, error)
	// RevokeCertificates 吊销节点所有未吊销、未过期的证书，返回被吊销的序列号。
	RevokeCertificates(ctx context.Context, nodeID int64, reason string, at time.Time) ([]string, error)
	// LastSuspectRevocation 返回这台节点最近一次因怀疑被攻破而吊销证书的时间（身份重复、管理员吊销，
	// 不含停用），没有过时 ok 为 false。之前签发的扣费凭证入账时记为待复核（设计 5.4）。
	LastSuspectRevocation(ctx context.Context, nodeID int64) (time.Time, bool, error)
	// ListRevokedSerials 返回 notAfter 之后才过期的已吊销证书（过期的证书握手本来就过不了）。
	ListRevokedSerials(ctx context.Context, notAfter time.Time) ([]string, error)
	Audit(ctx context.Context, e AuditEntry) error
}
