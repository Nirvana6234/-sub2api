package transport

import (
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// 主从通信的底层包不能依赖业务层（开发计划 2.1）：它们同时被主节点和从节点使用，
// 必须能单独编译、单独测试。master（主节点侧）和 node（从节点装配，要复用转发服务）
// 会用到业务层，不在检查范围；从节点"不连库"由 WP9 的对象图守卫保证。
var lowLevelRelayPackages = []string{"transport", "proto", "identity", "sealbox", "keystore", "relaytest", "sign", "nodestore"}

func TestRelayPackagesDoNotImportBusinessLayers(t *testing.T) {
	forbidden := []string{
		"github.com/Wei-Shaw/sub2api/internal/service",
		"github.com/Wei-Shaw/sub2api/internal/repository",
		"github.com/Wei-Shaw/sub2api/internal/handler",
		"github.com/Wei-Shaw/sub2api/ent",
	}
	fset := token.NewFileSet()
	for _, pkg := range lowLevelRelayPackages {
		err := filepath.WalkDir(filepath.Join("..", pkg), func(path string, d fs.DirEntry, err error) error {
			return checkRelayImports(t, fset, forbidden, path, d, err)
		})
		require.NoError(t, err)
	}
}

func checkRelayImports(t *testing.T, fset *token.FileSet, forbidden []string, path string, d fs.DirEntry, err error) error {
	t.Helper()
	if err != nil {
		return err
	}
	if d.IsDir() || !strings.HasSuffix(path, ".go") {
		return nil
	}
	f, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
	if err != nil {
		return err
	}
	for _, imp := range f.Imports {
		p, _ := strconv.Unquote(imp.Path.Value)
		for _, bad := range forbidden {
			if p == bad || strings.HasPrefix(p, bad+"/") {
				t.Errorf("%s imports %s", path, p)
			}
		}
	}
	return nil
}
