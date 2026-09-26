package master

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/relay/keystore"
	"github.com/Wei-Shaw/sub2api/internal/relay/proto/relayv1"
	"github.com/Wei-Shaw/sub2api/internal/relay/sign"
)

// Trust 是随配置快照下发给从节点的信任材料：根证书指纹、票据公钥（设计 7.4、8.1）。
type Trust struct {
	RootFingerprints []string
	TicketPublicKeys []*relayv1.SigningPublicKey
}

// deliveryIDs 返回这份信任材料里每一项的标识，用来判断某个新版本是否已送达节点。
func (t Trust) deliveryIDs() []string {
	ids := make([]string, 0, len(t.RootFingerprints)+len(t.TicketPublicKeys))
	for _, fp := range t.RootFingerprints {
		ids = append(ids, rootDeliveryID(fp))
	}
	for _, k := range t.TicketPublicKeys {
		ids = append(ids, ticketDeliveryID(k.Version, k.PublicKey))
	}
	return ids
}

func rootDeliveryID(fingerprint string) string { return "root:" + fingerprint }

func ticketDeliveryID(version uint32, pub []byte) string {
	return fmt.Sprintf("ticket:%d:%s", version, publicKeyFingerprint(pub))
}

// publicKeyFingerprint 是签名公钥的 SHA-256（十六进制），管理页展示、送达判断用。
func publicKeyFingerprint(pub []byte) string {
	sum := sha256.Sum256(pub)
	return hex.EncodeToString(sum[:])
}

// ErrSigningUnavailable：主从分流没在运行，签不了票据和凭证。
var ErrSigningUnavailable = errors.New("relay signing keys are not available; relay is not running")

// signingKeys 持有票据、凭证的签名器和公钥。启动时建立，轮换后重载。
type signingKeys struct {
	keys *keystore.Store

	mu            sync.RWMutex
	ticketSigner  *sign.Signer
	voucherSigner *sign.Signer
	ticketPub     *sign.PublicKeys
	ticketList    []*relayv1.SigningPublicKey
	voucherPub    *sign.PublicKeys
}

func loadSigningKeys(keys *keystore.Store) (*signingKeys, error) {
	s := &signingKeys{keys: keys}
	if err := s.reload(); err != nil {
		return nil, err
	}
	return s, nil
}

// reload 读取两种用途的当前版本（没有时生成）。
func (s *signingKeys) reload() error {
	ticketRing, err := s.keys.EnsureActive(keystore.PurposeTicket)
	if err != nil {
		return fmt.Errorf("load relay ticket key: %w", err)
	}
	voucherRing, err := s.keys.EnsureActive(keystore.PurposeVoucher)
	if err != nil {
		return fmt.Errorf("load relay voucher key: %w", err)
	}
	ticketSigner, err := sign.NewSigner(ticketRing.Active)
	if err != nil {
		return err
	}
	voucherSigner, err := sign.NewSigner(voucherRing.Active)
	if err != nil {
		return err
	}
	ticketPub, ticketList, err := sign.PublicKeysFromRing(ticketRing)
	if err != nil {
		return err
	}
	voucherPub, _, err := sign.PublicKeysFromRing(voucherRing)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.ticketSigner, s.voucherSigner = ticketSigner, voucherSigner
	s.ticketPub, s.ticketList, s.voucherPub = ticketPub, ticketList, voucherPub
	s.mu.Unlock()
	return nil
}

func (s *signingKeys) ticketPublicKeys() []*relayv1.SigningPublicKey {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]*relayv1.SigningPublicKey(nil), s.ticketList...)
}

// ---- 签发与验证（WP7 选号签凭证、WP8 入账验凭证、WP12 分配时签票据调用）----

func (r *Runtime) signing() (*signingKeys, error) {
	rr, err := r.runningRelay()
	if err != nil {
		return nil, ErrSigningUnavailable
	}
	return rr.signing, nil
}

// IssueTicket 给分配到 nodeID 的用户签发中转票据。tokenVersion 是用户当前的
// token_version（与登录态同一个值），选号时据此复查（设计 8.1）。
func (r *Runtime) IssueTicket(userID, nodeID, tokenVersion int64) (string, *relayv1.Ticket, error) {
	s, err := r.signing()
	if err != nil {
		return "", nil, err
	}
	s.mu.RLock()
	signer := s.ticketSigner
	s.mu.RUnlock()
	return sign.IssueTicket(signer, userID, nodeID, tokenVersion, r.now())
}

// IssueVoucher 签发一张扣费凭证（设计 5.3）。
func (r *Runtime) IssueVoucher(v *relayv1.Voucher) ([]byte, *relayv1.Voucher, error) {
	s, err := r.signing()
	if err != nil {
		return nil, nil, err
	}
	s.mu.RLock()
	signer := s.voucherSigner
	s.mu.RUnlock()
	return sign.IssueVoucher(signer, v, r.now())
}

// VerifyVoucher 验一张扣费凭证（入账时）。reportingNodeID 是上报这条记录的节点（取自主从连接的证书）。
func (r *Runtime) VerifyVoucher(raw []byte, reportingNodeID int64) (*relayv1.Voucher, error) {
	s, err := r.signing()
	if err != nil {
		return nil, err
	}
	s.mu.RLock()
	pub := s.voucherPub
	s.mu.RUnlock()
	return sign.VerifyVoucher(raw, pub, reportingNodeID, r.now())
}

// TicketPublicKeys 返回当前的票据公钥（测试和诊断用；从节点从配置快照取）。
func (r *Runtime) TicketPublicKeys() (*sign.PublicKeys, error) {
	s, err := r.signing()
	if err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.ticketPub, nil
}

// keyRetireWait 是某种用途的新版本签发满多久后才能停用旧版本：
// 根证书等旧根签的节点证书（24 小时）都过期；票据等旧票据都过期；
// 凭证等旧凭证都超过入账期限（60 天）。
func keyRetireWait(p keystore.Purpose) time.Duration {
	switch p {
	case keystore.PurposeRootCA:
		return rootRetireAfterActivation
	case keystore.PurposeTicket:
		return sign.TicketLifetime + sign.ClockSkew + time.Minute
	default:
		return sign.VoucherMaxAge + 24*time.Hour
	}
}

// keyNeedsDelivery：新版本启用前是否必须已送达所有在服务的节点。凭证公钥不下发，不需要。
func keyNeedsDelivery(p keystore.Purpose) bool {
	return p == keystore.PurposeRootCA || p == keystore.PurposeTicket
}
