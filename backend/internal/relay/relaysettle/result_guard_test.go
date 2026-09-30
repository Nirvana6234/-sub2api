package relaysettle

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

// fill 给每个导出字段填上非零值（递归到嵌套结构、指针、切片、映射）。
func fill(t *testing.T, v reflect.Value, n *int) {
	t.Helper()
	*n++
	switch v.Kind() {
	case reflect.String:
		v.SetString("s" + string(rune('a'+*n%26)))
	case reflect.Bool:
		v.SetBool(true)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		v.SetInt(int64(*n))
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		v.SetUint(uint64(*n))
	case reflect.Float32, reflect.Float64:
		v.SetFloat(float64(*n) + 0.5)
	case reflect.Pointer:
		v.Set(reflect.New(v.Type().Elem()))
		fill(t, v.Elem(), n)
	case reflect.Slice:
		s := reflect.MakeSlice(v.Type(), 1, 1)
		fill(t, s.Index(0), n)
		v.Set(s)
	case reflect.Map:
		m := reflect.MakeMap(v.Type())
		k := reflect.New(v.Type().Key()).Elem()
		fill(t, k, n)
		e := reflect.New(v.Type().Elem()).Elem()
		fill(t, e, n)
		m.SetMapIndex(k, e)
		v.Set(m)
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			if v.Type().Field(i).IsExported() {
				fill(t, v.Field(i), n)
			}
		}
	default:
		t.Fatalf("add a filler for %s", v.Type())
	}
}

// notTransmitted 是转发结果里不随扣费记录上报的字段（没有导出、只在 Responses WebSocket 的重放里用，
// WebSocket 还没接入主从分流；接入时要重新看）。
var notTransmitted = map[string]string{
	"wsReplayInput":                "websocket replay only",
	"wsReplayInputExists":          "websocket replay only",
	"wsAccountFailoverReplayInput": "websocket replay only",
}

// 扣费记录里的转发结果按 JSON 传给主节点：每个导出字段都要原样到达（新增字段带了 json:"-"
// 或者用了编码不了的类型，这里会失败）；不导出的字段必须写明为什么不用传。
func TestForwardResultSurvivesTheUsageRecord(t *testing.T) {
	var in service.OpenAIForwardResult
	n := 0
	fill(t, reflect.ValueOf(&in).Elem(), &n)
	raw, err := json.Marshal(in)
	require.NoError(t, err)
	var out service.OpenAIForwardResult
	require.NoError(t, json.Unmarshal(raw, &out))

	typ := reflect.TypeOf(in)
	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		if !f.IsExported() {
			_, ok := notTransmitted[f.Name]
			require.True(t, ok, "OpenAIForwardResult.%s is unexported and is lost on relay nodes: export it or explain why billing does not need it", f.Name)
			continue
		}
		require.Equal(t, reflect.ValueOf(in).Field(i).Interface(), reflect.ValueOf(out).Field(i).Interface(), "OpenAIForwardResult.%s does not survive the usage record", f.Name)
	}
}

// Anthropic Messages 的转发结果（service.ForwardResult）同样按 JSON 传：每个导出字段都要原样到达。
func TestAnthropicForwardResultSurvivesTheUsageRecord(t *testing.T) {
	var in service.ForwardResult
	n := 0
	fill(t, reflect.ValueOf(&in).Elem(), &n)
	raw, err := json.Marshal(in)
	require.NoError(t, err)
	var out service.ForwardResult
	require.NoError(t, json.Unmarshal(raw, &out))

	typ := reflect.TypeOf(in)
	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		require.True(t, f.IsExported(), "ForwardResult.%s is unexported and is lost on relay nodes", f.Name)
		require.Equal(t, reflect.ValueOf(in).Field(i).Interface(), reflect.ValueOf(out).Field(i).Interface(), "ForwardResult.%s does not survive the usage record", f.Name)
	}
}
