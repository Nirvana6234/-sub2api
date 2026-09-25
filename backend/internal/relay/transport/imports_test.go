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

// 主从通信层（internal/relay 下除 master 之外的包）不能依赖业务层：从节点不连库，
// 它的对象图里不能有 service / repository（开发计划 2.1 装配守卫，WP9）。
// master 包是主节点侧实现，允许依赖业务层，不在检查范围。
func TestRelayPackagesDoNotImportBusinessLayers(t *testing.T) {
	forbidden := []string{
		"github.com/Wei-Shaw/sub2api/internal/service",
		"github.com/Wei-Shaw/sub2api/internal/repository",
		"github.com/Wei-Shaw/sub2api/internal/handler",
		"github.com/Wei-Shaw/sub2api/ent",
	}
	root := ".."
	fset := token.NewFileSet()
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if filepath.Base(path) == "master" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
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
	})
	require.NoError(t, err)
}
