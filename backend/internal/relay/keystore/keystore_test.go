package keystore

import (
	"bytes"
	"crypto"
	"crypto/ed25519"
	"crypto/rand"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func testKEK(t *testing.T) []byte {
	kek := make([]byte, KEKLength)
	_, err := rand.Read(kek)
	require.NoError(t, err)
	return kek
}

func TestGenerateReloadAndSign(t *testing.T) {
	dir := t.TempDir()
	kek := testKEK(t)
	s, err := Open(dir, kek)
	require.NoError(t, err)

	ring, err := s.EnsureActive(PurposeTicket)
	require.NoError(t, err)
	require.Equal(t, 1, ring.Active.Version)

	// 重新打开后读到同一把钥匙。
	s2, err := Open(dir, kek)
	require.NoError(t, err)
	ring2, err := s2.Ring(PurposeTicket)
	require.NoError(t, err)
	msg := []byte("ticket")
	sig, err := ring2.Active.Signer.Sign(rand.Reader, msg, crypto.Hash(0))
	require.NoError(t, err)
	pub, ok := ring.Active.Public().(ed25519.PublicKey)
	require.True(t, ok)
	require.True(t, ed25519.Verify(pub, msg, sig))
}

func TestPrivateKeyIsNotStoredInPlaintextAndNeedsTheKEK(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir, testKEK(t))
	require.NoError(t, err)
	key, err := s.Generate(PurposeVoucher)
	require.NoError(t, err)

	raw, err := os.ReadFile(filepath.Join(dir, "voucher-v1.json"))
	require.NoError(t, err)
	priv, ok := key.Signer.(ed25519.PrivateKey)
	require.True(t, ok)
	seed := priv.Seed()
	require.False(t, bytes.Contains(raw, seed), "private key must not appear in the file")

	wrong, err := Open(dir, testKEK(t))
	require.NoError(t, err)
	_, err = wrong.Ring(PurposeVoucher)
	require.ErrorContains(t, err, "wrong key encryption key")
}

func TestTamperedFilesAreRejected(t *testing.T) {
	dir := t.TempDir()
	kek := testKEK(t)
	s, err := Open(dir, kek)
	require.NoError(t, err)
	_, err = s.Generate(PurposeTicket)
	require.NoError(t, err)
	_, err = s.Generate(PurposeTicket)
	require.NoError(t, err)

	// 把 v2 的内容复制成 v1：文件名和内容不符，解密的附加数据也对不上。
	v2, err := os.ReadFile(filepath.Join(dir, "ticket-v2.json"))
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "ticket-v1.json"), v2, 0o600))
	_, err = s.Ring(PurposeTicket)
	require.Error(t, err)
}

func TestRootCARotationKeepsOldVersionForVerificationUntilRetired(t *testing.T) {
	s, err := Open(t.TempDir(), testKEK(t))
	require.NoError(t, err)
	first, err := s.EnsureActive(PurposeRootCA)
	require.NoError(t, err)
	require.NotNil(t, first.Active.Certificate)
	require.True(t, first.Active.Certificate.IsCA)

	_, err = s.Generate(PurposeRootCA)
	require.NoError(t, err)
	ring, err := s.Ring(PurposeRootCA)
	require.NoError(t, err)
	require.Equal(t, 2, ring.Active.Version)
	require.Len(t, ring.Keys, 2)

	require.NoError(t, s.Retire(PurposeRootCA, 1))
	ring, err = s.Ring(PurposeRootCA)
	require.NoError(t, err)
	require.Len(t, ring.Keys, 1)
	require.Error(t, s.Retire(PurposeRootCA, 2), "the only active key cannot be retired")
}

func TestParseKEK(t *testing.T) {
	_, err := ParseKEK("abcd")
	require.Error(t, err)
	kek, err := ParseKEK(" " + string(bytes.Repeat([]byte("ab"), 32)) + " ")
	require.NoError(t, err)
	require.Len(t, kek, KEKLength)
}

// 轮换分三步：预备（公开、参与验证、不签发）→ 启用（签发）→ 停用旧版本。
func TestStagedRootIsPublishedBeforeItSigns(t *testing.T) {
	s, err := Open(t.TempDir(), testKEK(t))
	require.NoError(t, err)
	first, err := s.EnsureActive(PurposeRootCA)
	require.NoError(t, err)

	staged, err := s.Stage(PurposeRootCA)
	require.NoError(t, err)
	require.True(t, staged.Staged)
	ring, err := s.Ring(PurposeRootCA)
	require.NoError(t, err)
	require.Len(t, ring.Keys, 2, "the staged root is already used for verification")
	require.Equal(t, first.Active.Version, ring.Active.Version, "but the old root still signs")
	require.Error(t, s.Retire(PurposeRootCA, first.Active.Version), "the only activated root cannot be retired")

	require.NoError(t, s.Activate(PurposeRootCA, staged.Version))
	ring, err = s.Ring(PurposeRootCA)
	require.NoError(t, err)
	require.Equal(t, staged.Version, ring.Active.Version)
	require.NotNil(t, ring.Active.ActivatedAt)

	require.NoError(t, s.Retire(PurposeRootCA, first.Active.Version))
	ring, err = s.Ring(PurposeRootCA)
	require.NoError(t, err)
	require.Len(t, ring.Keys, 1)
	require.Error(t, s.Activate(PurposeRootCA, first.Active.Version), "a retired key cannot come back")
}

func TestStageRefusesSecondStagedAndActivationSurvivesReopen(t *testing.T) {
	dir, kek := t.TempDir(), testKEK(t)
	s, err := Open(dir, kek)
	require.NoError(t, err)
	_, err = s.EnsureActive(PurposeRootCA)
	require.NoError(t, err)
	staged, err := s.Stage(PurposeRootCA)
	require.NoError(t, err)
	_, err = s.Stage(PurposeRootCA)
	require.ErrorIs(t, err, ErrAlreadyStaged)

	require.NoError(t, s.Activate(PurposeRootCA, staged.Version))
	reopened, err := Open(dir, kek)
	require.NoError(t, err)
	ring, err := reopened.Ring(PurposeRootCA)
	require.NoError(t, err)
	require.Equal(t, staged.Version, ring.Active.Version)
	require.False(t, ring.Active.Staged)
	require.NotNil(t, ring.Active.ActivatedAt, "the retire-after-activation wait must survive a restart")
	for _, k := range ring.Keys {
		require.NotNil(t, k.ActivatedAt, "v%d", k.Version)
	}

	_, err = reopened.Stage(PurposeRootCA)
	require.NoError(t, err, "a new staged key is allowed once the previous one is activated")
}
