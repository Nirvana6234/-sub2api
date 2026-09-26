// Package keystore 保管主节点的三把关键私钥（设计 7.4）：主从通信根证书、
// 签中转票据、签扣费凭证。
//
//   - 私钥加密后存成文件，加密密钥（KEK）来自主节点的环境变量或单独文件，**不进数据库**：
//     现有备份功能会把数据库备份到对象存储，私钥在库里就会跟着进备份。
//   - 目录可以整个离线备份：文件本身是密文，KEK 另行保管。
//   - 轮换时新旧版本并存：新版本用于签发，旧版本只用于验证，等从节点都拿到新公钥后
//     再把旧版本标记为停用。
package keystore

import (
	"crypto"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Purpose 是私钥的用途。
type Purpose string

const (
	PurposeRootCA  Purpose = "root_ca"
	PurposeTicket  Purpose = "ticket"
	PurposeVoucher Purpose = "voucher"
)

// KEKLength 是加密密钥的长度（AES-256）。
const KEKLength = 32

// ParseKEK 解析十六进制的加密密钥。
func ParseKEK(hexKey string) ([]byte, error) {
	kek, err := hex.DecodeString(strings.TrimSpace(hexKey))
	if err != nil {
		return nil, fmt.Errorf("keystore: key encryption key must be hex: %w", err)
	}
	if len(kek) != KEKLength {
		return nil, fmt.Errorf("keystore: key encryption key must be %d bytes, got %d", KEKLength, len(kek))
	}
	return kek, nil
}

// Key 是一个版本的私钥。
type Key struct {
	Purpose   Purpose
	Version   int
	CreatedAt time.Time
	// Staged 为 true 表示预备中：用于验证、公开（比如根证书指纹随配置下发给从节点），
	// 但还不用于签发。轮换第一步生成预备版本，所有节点都拿到后再 Activate。
	Staged      bool
	ActivatedAt *time.Time
	Signer      crypto.Signer
	// Certificate 只有根证书用途有：自签的主从通信根证书。
	Certificate *x509.Certificate
}

// Public 返回公钥。
func (k *Key) Public() crypto.PublicKey { return k.Signer.Public() }

// Ring 是一个用途下所有未停用的版本。Active 是最新的已启用版本，用于签发；
// Keys（含预备版本）全部用于验证。
type Ring struct {
	Active *Key
	Keys   []*Key
}

// Store 是加密私钥文件的目录。
type Store struct {
	dir string
	kek []byte
	mu  sync.Mutex
	now func() time.Time
}

// Open 打开（必要时创建）私钥目录。
func Open(dir string, kek []byte) (*Store, error) {
	if len(kek) != KEKLength {
		return nil, fmt.Errorf("keystore: key encryption key must be %d bytes", KEKLength)
	}
	if strings.TrimSpace(dir) == "" {
		return nil, errors.New("keystore: directory is required")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	return &Store{dir: dir, kek: append([]byte(nil), kek...), now: time.Now}, nil
}

type keyFile struct {
	Format      int        `json:"format"`
	Purpose     Purpose    `json:"purpose"`
	Version     int        `json:"version"`
	Algorithm   string     `json:"algorithm"`
	CreatedAt   time.Time  `json:"created_at"`
	RetiredAt   *time.Time `json:"retired_at,omitempty"`
	Staged      bool       `json:"staged,omitempty"`
	ActivatedAt *time.Time `json:"activated_at,omitempty"`
	PublicKey   []byte     `json:"public_key"`
	Nonce       []byte     `json:"nonce"`
	Ciphertext  []byte     `json:"ciphertext"`
	// Certificate 是根证书的 DER（公开信息，不加密）。
	Certificate []byte `json:"certificate,omitempty"`
}

func (s *Store) path(p Purpose, version int) string {
	return filepath.Join(s.dir, fmt.Sprintf("%s-v%d.json", p, version))
}

func aad(p Purpose, version int) []byte {
	return []byte(fmt.Sprintf("sub2api-relay-key|%s|v%d", p, version))
}

// Ring 读出某个用途的所有未停用版本。没有任何版本时返回 Active 为 nil 的空 Ring。
func (s *Store) Ring(p Purpose) (*Ring, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	files, err := s.list(p)
	if err != nil {
		return nil, err
	}
	ring := &Ring{}
	for _, f := range files {
		if f.RetiredAt != nil {
			continue
		}
		k, err := s.decode(f)
		if err != nil {
			return nil, err
		}
		ring.Keys = append(ring.Keys, k)
	}
	for i := len(ring.Keys) - 1; i >= 0; i-- {
		if !ring.Keys[i].Staged {
			ring.Active = ring.Keys[i]
			break
		}
	}
	return ring, nil
}

// Generate 生成一个新版本并直接启用（设为签发用的版本）。根证书用途同时生成 20 年有效的自签根证书。
func (s *Store) Generate(p Purpose) (*Key, error) { return s.generate(p, false) }

// Stage 生成一个预备版本：参与验证、公开，但不用于签发，等 Activate。
// 同一用途同时只能有一个预备版本，已有时返回 ErrAlreadyStaged。
func (s *Store) Stage(p Purpose) (*Key, error) { return s.generate(p, true) }

// ErrAlreadyStaged：这个用途已有一个预备版本，先启用或停用它。
var ErrAlreadyStaged = errors.New("keystore: a staged key already exists; activate or retire it first")

func (s *Store) generate(p Purpose, staged bool) (*Key, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	files, err := s.list(p)
	if err != nil {
		return nil, err
	}
	if staged {
		for _, f := range files {
			if f.Staged && f.RetiredAt == nil {
				return nil, ErrAlreadyStaged
			}
		}
	}
	version := 1
	if n := len(files); n > 0 {
		version = files[n-1].Version + 1
	}
	now := s.now().UTC()

	var signer crypto.Signer
	var algorithm string
	switch p {
	case PurposeRootCA:
		k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			return nil, err
		}
		signer, algorithm = k, "ecdsa-p256"
	case PurposeTicket, PurposeVoucher:
		_, k, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return nil, err
		}
		signer, algorithm = k, "ed25519"
	default:
		return nil, fmt.Errorf("keystore: unknown purpose %q", p)
	}

	der, err := x509.MarshalPKCS8PrivateKey(signer)
	if err != nil {
		return nil, err
	}
	pub, err := x509.MarshalPKIXPublicKey(signer.Public())
	if err != nil {
		return nil, err
	}
	nonce, ciphertext, err := s.seal(der, aad(p, version))
	if err != nil {
		return nil, err
	}
	f := keyFile{Format: 1, Purpose: p, Version: version, Algorithm: algorithm, CreatedAt: now, PublicKey: pub, Nonce: nonce, Ciphertext: ciphertext, Staged: staged}
	if !staged {
		f.ActivatedAt = &now
	}
	key := &Key{Purpose: p, Version: version, CreatedAt: now, Staged: staged, ActivatedAt: f.ActivatedAt, Signer: signer}
	if p == PurposeRootCA {
		cert, err := selfSignRoot(signer, version, now)
		if err != nil {
			return nil, err
		}
		f.Certificate = cert.Raw
		key.Certificate = cert
	}
	if err := s.write(f); err != nil {
		return nil, err
	}
	return key, nil
}

// EnsureActive 返回某个用途的 Ring；一个版本都没有时先生成第一个。
func (s *Store) EnsureActive(p Purpose) (*Ring, error) {
	ring, err := s.Ring(p)
	if err != nil {
		return nil, err
	}
	if ring.Active != nil {
		return ring, nil
	}
	if _, err := s.Generate(p); err != nil {
		return nil, err
	}
	return s.Ring(p)
}

// Activate 启用一个预备版本：之后签发用它（它是最新的已启用版本时）。
func (s *Store) Activate(p Purpose, version int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	files, err := s.list(p)
	if err != nil {
		return err
	}
	for i := range files {
		if files[i].Version != version {
			continue
		}
		if files[i].RetiredAt != nil {
			return fmt.Errorf("keystore: %s v%d is retired", p, version)
		}
		if !files[i].Staged {
			return nil
		}
		now := s.now().UTC()
		files[i].Staged = false
		files[i].ActivatedAt = &now
		return s.write(files[i])
	}
	return fmt.Errorf("keystore: %s v%d not found", p, version)
}

// Retire 把某个版本标记为停用：不再用于验证。不能停用最后一个已启用的版本。
func (s *Store) Retire(p Purpose, version int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	files, err := s.list(p)
	if err != nil {
		return err
	}
	var target *keyFile
	active := 0
	for i := range files {
		if files[i].RetiredAt == nil && !files[i].Staged {
			active++
		}
		if files[i].Version == version {
			target = &files[i]
		}
	}
	if target == nil {
		return fmt.Errorf("keystore: %s v%d not found", p, version)
	}
	if target.RetiredAt != nil {
		return nil
	}
	if active <= 1 && !target.Staged {
		return fmt.Errorf("keystore: cannot retire the only active %s key", p)
	}
	now := s.now().UTC()
	target.RetiredAt = &now
	return s.write(*target)
}

func (s *Store) list(p Purpose) ([]keyFile, error) {
	matches, err := filepath.Glob(filepath.Join(s.dir, string(p)+"-v*.json"))
	if err != nil {
		return nil, err
	}
	out := make([]keyFile, 0, len(matches))
	for _, m := range matches {
		raw, err := os.ReadFile(m)
		if err != nil {
			return nil, err
		}
		var f keyFile
		if err := json.Unmarshal(raw, &f); err != nil {
			return nil, fmt.Errorf("keystore: %s: %w", filepath.Base(m), err)
		}
		if f.Purpose != p || filepath.Base(m) != filepath.Base(s.path(p, f.Version)) {
			return nil, fmt.Errorf("keystore: %s does not match its contents", filepath.Base(m))
		}
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Version < out[j].Version })
	return out, nil
}

func (s *Store) decode(f keyFile) (*Key, error) {
	der, err := s.open(f.Nonce, f.Ciphertext, aad(f.Purpose, f.Version))
	if err != nil {
		return nil, fmt.Errorf("keystore: cannot decrypt %s v%d (wrong key encryption key?): %w", f.Purpose, f.Version, err)
	}
	parsed, err := x509.ParsePKCS8PrivateKey(der)
	if err != nil {
		return nil, err
	}
	signer, ok := parsed.(crypto.Signer)
	if !ok {
		return nil, errors.New("keystore: stored key is not a signer")
	}
	pub, err := x509.MarshalPKIXPublicKey(signer.Public())
	if err != nil {
		return nil, err
	}
	if string(pub) != string(f.PublicKey) {
		return nil, fmt.Errorf("keystore: %s v%d public key does not match its private key", f.Purpose, f.Version)
	}
	key := &Key{Purpose: f.Purpose, Version: f.Version, CreatedAt: f.CreatedAt, Staged: f.Staged, ActivatedAt: f.ActivatedAt, Signer: signer}
	if !key.Staged && key.ActivatedAt == nil {
		// 加入预备状态之前写的文件没有启用时间：它们生成即启用。
		created := f.CreatedAt
		key.ActivatedAt = &created
	}
	if len(f.Certificate) > 0 {
		cert, err := x509.ParseCertificate(f.Certificate)
		if err != nil {
			return nil, err
		}
		certPub, err := x509.MarshalPKIXPublicKey(cert.PublicKey)
		if err != nil || string(certPub) != string(pub) {
			return nil, fmt.Errorf("keystore: %s v%d certificate does not match its key", f.Purpose, f.Version)
		}
		key.Certificate = cert
	}
	return key, nil
}

func (s *Store) seal(plaintext, additional []byte) (nonce, ciphertext []byte, err error) {
	aead, err := s.aead()
	if err != nil {
		return nil, nil, err
	}
	nonce = make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, nil, err
	}
	return nonce, aead.Seal(nil, nonce, plaintext, additional), nil
}

func (s *Store) open(nonce, ciphertext, additional []byte) ([]byte, error) {
	aead, err := s.aead()
	if err != nil {
		return nil, err
	}
	return aead.Open(nil, nonce, ciphertext, additional)
}

func (s *Store) aead() (cipher.AEAD, error) {
	block, err := aes.NewCipher(s.kek)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// write 先写临时文件再改名，避免写一半留下坏文件。
func (s *Store) write(f keyFile) error {
	raw, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	final := s.path(f.Purpose, f.Version)
	tmp := final + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, final)
}

func selfSignRoot(signer crypto.Signer, version int, now time.Time) (*x509.Certificate, error) {
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
	if err != nil {
		return nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: fmt.Sprintf("sub2api relay root v%d", version)},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(20 * 365 * 24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		MaxPathLenZero:        true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, signer.Public(), signer)
	if err != nil {
		return nil, err
	}
	return x509.ParseCertificate(der)
}
