package sealbox

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSealOpenRoundTrip(t *testing.T) {
	node, err := GenerateKey()
	require.NoError(t, err)
	secret := []byte("sk-upstream-credential-value")
	aad := []byte("account:12|version:3")

	sealed, err := Seal(node.PublicKey(), secret, aad)
	require.NoError(t, err)
	require.False(t, bytes.Contains(sealed, secret))

	got, err := Open(node, sealed, aad)
	require.NoError(t, err)
	require.Equal(t, secret, got)

	// 每次加密都不同（临时密钥和 nonce 随机）。
	again, err := Seal(node.PublicKey(), secret, aad)
	require.NoError(t, err)
	require.NotEqual(t, sealed, again)
}

func TestOpenRejectsWrongKeyWrongAADAndTampering(t *testing.T) {
	node, err := GenerateKey()
	require.NoError(t, err)
	other, err := GenerateKey()
	require.NoError(t, err)
	sealed, err := Seal(node.PublicKey(), []byte("secret"), []byte("account:12|version:3"))
	require.NoError(t, err)

	_, err = Open(other, sealed, []byte("account:12|version:3"))
	require.Error(t, err, "another node's key cannot open it")

	_, err = Open(node, sealed, []byte("account:13|version:3"))
	require.Error(t, err, "ciphertext cannot be reused for another account")

	for i := range sealed {
		tampered := append([]byte(nil), sealed...)
		tampered[i] ^= 0x01
		_, err = Open(node, tampered, []byte("account:12|version:3"))
		require.Errorf(t, err, "flipping byte %d must be detected", i)
	}

	_, err = Open(node, sealed[:10], nil)
	require.Error(t, err)
}

func TestParsePublicKey(t *testing.T) {
	node, err := GenerateKey()
	require.NoError(t, err)
	pub, err := ParsePublicKey(node.PublicKey().Bytes())
	require.NoError(t, err)
	require.True(t, pub.Equal(node.PublicKey()))
	_, err = ParsePublicKey([]byte("short"))
	require.Error(t, err)
}
