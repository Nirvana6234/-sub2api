//go:build unit

package service

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

// 改了设置之后调用 InvalidateAll，下一次读取立即拿到新值，不等缓存 TTL。
func TestInvalidateAllMakesTheNextReadSeeNewSettings(t *testing.T) {
	repo := &mapSettingRepo{values: map[string]string{
		SettingKeyMinClaudeCodeVersion: "1.0.0",
		SettingKeyGlobalBlacklist:      "[]",
	}}
	svc := NewSettingService(repo, &config.Config{})
	ctx := context.Background()

	minV, _ := svc.GetClaudeCodeVersionBounds(ctx)
	require.Equal(t, "1.0.0", minV)
	value, err := svc.hotSettings().GetValue(ctx, SettingKeyMinClaudeCodeVersion)
	require.NoError(t, err)
	require.Equal(t, "1.0.0", value)

	repo.values[SettingKeyMinClaudeCodeVersion] = "2.0.0"
	minV, _ = svc.GetClaudeCodeVersionBounds(ctx)
	require.Equal(t, "1.0.0", minV, "still served from the cache")

	svc.InvalidateAll()
	minV, _ = svc.GetClaudeCodeVersionBounds(ctx)
	require.Equal(t, "2.0.0", minV)
	value, err = svc.hotSettings().GetValue(ctx, SettingKeyMinClaudeCodeVersion)
	require.NoError(t, err)
	require.Equal(t, "2.0.0", value)

	// 清空之后各个读取都不能崩（有的缓存读取不做空指针检查）。
	_, err = svc.GlobalBlacklistSnapshot(ctx)
	require.NoError(t, err)
	_ = svc.GetOpenAIQuotaAutoPauseSettings(ctx)
	_ = svc.GetOpenAITTFTMode(ctx)
}

// 包级的 atomic.Value 缓存变量、SettingService 里的缓存字段，都必须在 InvalidateAll 里被清掉，
// 或者在这里登记不清的理由。新增设置缓存时漏了，从节点的版本栅栏就会失效。
func TestInvalidateAllCoversEverySettingCache(t *testing.T) {
	notSettingCaches := map[string]string{
		"moderationProxyCache": "ContentModerationService 自己的代理地址缓存；内容审核在主节点执行（设计 3.4）",
	}

	fset := token.NewFileSet()
	files, err := filepath.Glob("*.go")
	require.NoError(t, err)
	caches := map[string]string{}
	var invalidateBody string
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(name)
		require.NoError(t, err)
		f, err := parser.ParseFile(fset, name, src, 0)
		require.NoError(t, err)
		for _, decl := range f.Decls {
			switch d := decl.(type) {
			case *ast.GenDecl:
				for _, spec := range d.Specs {
					switch sp := spec.(type) {
					case *ast.ValueSpec:
						if isAtomicCacheType(sp.Type) {
							for _, n := range sp.Names {
								caches[n.Name] = name
							}
						}
					case *ast.TypeSpec:
						st, ok := sp.Type.(*ast.StructType)
						if !ok || sp.Name.Name != "SettingService" {
							continue
						}
						for _, field := range st.Fields.List {
							if isAtomicCacheType(field.Type) {
								for _, n := range field.Names {
									caches[n.Name] = name
								}
							}
						}
					}
				}
			case *ast.FuncDecl:
				if d.Name.Name == "InvalidateAll" && d.Recv != nil {
					invalidateBody = string(src[fset.Position(d.Body.Pos()).Offset:fset.Position(d.Body.End()).Offset])
				}
			}
		}
	}
	require.NotEmpty(t, invalidateBody, "InvalidateAll not found")
	require.NotEmpty(t, caches)
	for name, file := range caches {
		if !strings.HasSuffix(strings.ToLower(name), "cache") {
			continue
		}
		if _, skip := notSettingCaches[name]; skip {
			continue
		}
		require.Containsf(t, invalidateBody, name, "%s (%s) is not reset by SettingService.InvalidateAll", name, file)
	}
}

func isAtomicCacheType(expr ast.Expr) bool {
	switch e := expr.(type) {
	case *ast.SelectorExpr:
		pkg, ok := e.X.(*ast.Ident)
		return ok && pkg.Name == "atomic" && e.Sel.Name == "Value"
	case *ast.IndexExpr:
		sel, ok := e.X.(*ast.SelectorExpr)
		if !ok {
			return false
		}
		pkg, ok := sel.X.(*ast.Ident)
		return ok && pkg.Name == "atomic" && sel.Sel.Name == "Pointer"
	}
	return false
}

// mapSettingRepo 是完整实现 SettingRepository 的内存仓储。
type mapSettingRepo struct {
	values map[string]string
}

func (r *mapSettingRepo) Get(_ context.Context, key string) (*Setting, error) {
	v, ok := r.values[key]
	if !ok {
		return nil, ErrSettingNotFound
	}
	return &Setting{Key: key, Value: v}, nil
}

func (r *mapSettingRepo) GetValue(_ context.Context, key string) (string, error) {
	v, ok := r.values[key]
	if !ok {
		return "", ErrSettingNotFound
	}
	return v, nil
}

func (r *mapSettingRepo) Set(_ context.Context, key, value string) error {
	r.values[key] = value
	return nil
}

func (r *mapSettingRepo) GetMultiple(_ context.Context, keys []string) (map[string]string, error) {
	out := make(map[string]string, len(keys))
	for _, k := range keys {
		if v, ok := r.values[k]; ok {
			out[k] = v
		}
	}
	return out, nil
}

func (r *mapSettingRepo) SetMultiple(_ context.Context, settings map[string]string) error {
	for k, v := range settings {
		r.values[k] = v
	}
	return nil
}

func (r *mapSettingRepo) GetAll(context.Context) (map[string]string, error) {
	out := make(map[string]string, len(r.values))
	for k, v := range r.values {
		out[k] = v
	}
	return out, nil
}

func (r *mapSettingRepo) Delete(_ context.Context, key string) error {
	delete(r.values, key)
	return nil
}
