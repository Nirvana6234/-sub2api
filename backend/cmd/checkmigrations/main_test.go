package main

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLoadProdChecksumsParsesFilenamePipeChecksumLines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "prod.txt")
	require.NoError(t, os.WriteFile(path, []byte("001_init.sql|abc123\n\n002_next.sql|def456\nmalformed_line_without_pipe\n"), 0o644))

	got, err := loadProdChecksums(path)
	require.NoError(t, err)
	require.Equal(t, map[string]string{
		"001_init.sql": "abc123",
		"002_next.sql": "def456",
	}, got)
}

func TestLoadLocalChecksumsMatchesTrimSpaceSHA256(t *testing.T) {
	dir := t.TempDir()
	// 前后带空白/换行，验证按 TrimSpace 之后的内容算校验和，
	// 必须跟 migrations_runner.go 的 ApplyMigrations 完全一致，否则这个工具
	// 会把"其实没变"的文件误报成 MISMATCH。
	require.NoError(t, os.WriteFile(filepath.Join(dir, "001_init.sql"), []byte("\n\nselect 1;\n\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "not_sql.txt"), []byte("ignored"), 0o644))

	got, err := loadLocalChecksums(dir)
	require.NoError(t, err)

	sum := sha256.Sum256([]byte("select 1;"))
	want := hex.EncodeToString(sum[:])
	require.Equal(t, map[string]string{"001_init.sql": want}, got)
}

func TestLoadLocalChecksumsSkipsEmptyFiles(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "999_empty.sql"), []byte("   \n\n  "), 0o644))

	got, err := loadLocalChecksums(dir)
	require.NoError(t, err)
	require.Empty(t, got)
}
