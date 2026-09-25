// Package sealbox 给上游凭据再加一层加密（设计 7.1 第 4 条）：主节点用从节点的
// X25519 加密公钥加密，只有那台从节点能解开。即使主从之间被放了会解密 TLS 的代理，
// 凭据也不会以明文出现。
//
// 做法：每次加密生成一对临时 X25519 密钥，与从节点公钥做 ECDH，
// 经 HKDF-SHA256 派生 AES-256-GCM 密钥。附加数据（AAD）绑定用途，
// 比如"账号 12 的凭据版本 3"，密文不能被挪作他用。
//
// 签名用的证书密钥（Ed25519 / ECDSA）不能直接用来加密，所以从节点另有一对 X25519
// 加密密钥，每次续签证书时一起更换。
package sealbox

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
)

const (
	version   = 1
	keyLength = 32
	hkdfInfo  = "sub2api relay sealbox v1"
)

// 密文格式：version(1) | 临时公钥(32) | nonce(12) | AES-GCM 密文。
const headerLen = 1 + keyLength + 12

// GenerateKey 生成一对 X25519 加密密钥。
func GenerateKey() (*ecdh.PrivateKey, error) {
	return ecdh.X25519().GenerateKey(rand.Reader)
}

// ParsePublicKey 解析 32 字节的 X25519 公钥。
func ParsePublicKey(raw []byte) (*ecdh.PublicKey, error) {
	if len(raw) != keyLength {
		return nil, fmt.Errorf("sealbox: public key must be %d bytes", keyLength)
	}
	return ecdh.X25519().NewPublicKey(raw)
}

// Seal 用接收方公钥加密 plaintext。aad 必须在解密时原样提供。
func Seal(recipient *ecdh.PublicKey, plaintext, aad []byte) ([]byte, error) {
	if recipient == nil {
		return nil, errors.New("sealbox: missing recipient key")
	}
	eph, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	shared, err := eph.ECDH(recipient)
	if err != nil {
		return nil, err
	}
	ephPub := eph.PublicKey().Bytes()
	aead, err := deriveAEAD(shared, ephPub, recipient.Bytes())
	if err != nil {
		return nil, err
	}
	out := make([]byte, headerLen, headerLen+len(plaintext)+aead.Overhead())
	out[0] = version
	copy(out[1:], ephPub)
	nonce := out[1+keyLength : headerLen]
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return aead.Seal(out, nonce, plaintext, fullAAD(out[:headerLen], aad)), nil
}

// Open 用接收方私钥解密 Seal 的结果。
func Open(recipient *ecdh.PrivateKey, sealed, aad []byte) ([]byte, error) {
	if recipient == nil {
		return nil, errors.New("sealbox: missing recipient key")
	}
	if len(sealed) < headerLen+16 || sealed[0] != version {
		return nil, errors.New("sealbox: malformed ciphertext")
	}
	ephPub, err := ecdh.X25519().NewPublicKey(sealed[1 : 1+keyLength])
	if err != nil {
		return nil, err
	}
	shared, err := recipient.ECDH(ephPub)
	if err != nil {
		return nil, err
	}
	aead, err := deriveAEAD(shared, sealed[1:1+keyLength], recipient.PublicKey().Bytes())
	if err != nil {
		return nil, err
	}
	plaintext, err := aead.Open(nil, sealed[1+keyLength:headerLen], sealed[headerLen:], fullAAD(sealed[:headerLen], aad))
	if err != nil {
		return nil, errors.New("sealbox: decryption failed")
	}
	return plaintext, nil
}

// deriveAEAD 从共享密钥派生 AES-256-GCM。盐里带上双方公钥，派生结果绑定这次交换。
func deriveAEAD(shared, ephPub, recipientPub []byte) (cipher.AEAD, error) {
	salt := make([]byte, 0, 2*keyLength)
	salt = append(salt, ephPub...)
	salt = append(salt, recipientPub...)
	key, err := hkdf.Key(sha256.New, shared, salt, hkdfInfo, 32)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func fullAAD(header, aad []byte) []byte {
	out := make([]byte, 0, len(header)+len(aad))
	out = append(out, header...)
	return append(out, aad...)
}
