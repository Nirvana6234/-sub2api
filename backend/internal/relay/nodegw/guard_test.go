package nodegw

import (
	"database/sql"
	"reflect"
	"strings"
	"testing"
	"unsafe"

	"github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/relay/node"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

// 装配守卫（开发计划 WP9）：从节点处理函数的对象图里不能有数据库或 Redis 客户端——从节点不连库，
// 转发路径上要是有谁拿着它们，就说明有依赖没换成远程实现。
func TestNodeObjectGraphHasNoDatabaseOrRedis(t *testing.T) {
	cfg := &config.Config{}
	d := NewDispatcher(Deps{})
	decider, reporter := node.NewRemoteUpstreamErrorDecider(nil), node.NewRemoteAccountReporter(node.NewEventOutbox(0))
	deps := GatewayDeps{
		Config: cfg, Settings: service.NewSettingService(node.NewConfigCache(), cfg), Dispatcher: d,
		Decider: decider, Reporter: reporter,
	}
	h := struct {
		OpenAI    any
		Anthropic any
	}{
		NewOpenAIHandler(deps),
		NewAnthropicHandler(deps, AnthropicDeps{AccountState: node.NewRemoteAccountState(decider, reporter), TempUnschedulable: reporter.TempUnschedulable, MaskedSession: reporter.MaskedSession}),
	}
	forbidden := []reflect.Type{
		reflect.TypeOf((*ent.Client)(nil)),
		reflect.TypeOf((*sql.DB)(nil)),
		reflect.TypeOf((*redis.Client)(nil)),
		reflect.TypeOf((*redis.ClusterClient)(nil)),
	}
	var found []string
	walk(reflect.ValueOf(h), "handler", map[uintptr]bool{}, func(path string, v reflect.Value) {
		for _, f := range forbidden {
			if v.Type() == f && !v.IsNil() {
				found = append(found, path+": "+f.String())
			}
		}
		if v.Kind() == reflect.Interface && !v.IsNil() && strings.Contains(v.Elem().Type().PkgPath(), "internal/repository") {
			found = append(found, path+": "+v.Elem().Type().String())
		}
	})
	require.Empty(t, found)
}

// walk 深度遍历（含未导出字段），指针只走一次。
func walk(v reflect.Value, path string, seen map[uintptr]bool, visit func(string, reflect.Value)) {
	if !v.IsValid() {
		return
	}
	visit(path, v)
	switch v.Kind() {
	case reflect.Pointer:
		if v.IsNil() || seen[v.Pointer()] {
			return
		}
		seen[v.Pointer()] = true
		walk(v.Elem(), path, seen, visit)
	case reflect.Interface:
		if !v.IsNil() {
			walk(v.Elem(), path, seen, visit)
		}
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			f := v.Field(i)
			if !f.CanInterface() && f.CanAddr() {
				f = reflect.NewAt(f.Type(), unsafe.Pointer(f.UnsafeAddr())).Elem()
			}
			walk(f, path+"."+v.Type().Field(i).Name, seen, visit)
		}
	case reflect.Slice, reflect.Array:
		for i := 0; i < v.Len() && i < 64; i++ {
			walk(v.Index(i), path+"[]", seen, visit)
		}
	case reflect.Map:
		iter := v.MapRange()
		for n := 0; iter.Next() && n < 64; n++ {
			walk(iter.Value(), path+"{}", seen, visit)
		}
	}
}
