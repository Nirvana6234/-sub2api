package relayselect

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/relay/proto/relayv1"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// memTaskStore 是主节点上的异步图片任务存储（真实现在 Redis）。
type memTaskStore struct {
	mu    sync.Mutex
	tasks map[string]service.ImageTaskRecord
}

func (s *memTaskStore) Save(_ context.Context, task *service.ImageTaskRecord, _ time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.tasks == nil {
		s.tasks = map[string]service.ImageTaskRecord{}
	}
	s.tasks[task.ID] = *task
	return nil
}

func (s *memTaskStore) Get(_ context.Context, id string) (*service.ImageTaskRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tasks[id]
	if !ok {
		return nil, service.ErrImageTaskNotFound
	}
	return &t, nil
}

// 异步图片任务经从节点：提交在从节点执行（选号、扣费照同步图片入口），任务状态在主节点；轮询（也落在从节点）向主节点查，
// 别的 Key 查不到；功能在主节点没开时回 404。
func TestNodeRunsAsyncImageTasksWithTheTaskStateOnTheMaster(t *testing.T) {
	accounts := []service.Account{apiKeyAccount(1, "one")}
	e := startStandardE2E(t, func(upstream string) []service.Account {
		accounts[0].Credentials["base_url"] = upstream
		accounts[0].AccountGroups = []service.AccountGroup{{AccountID: 1, GroupID: 5}}
		return accounts
	})
	e.world.keys.keys["sk-a"].Group.AllowImageGeneration = true

	// 主节点没开：404。
	status, body := e.post(t, "/v1/images/generations/async", "sk-a", imagesBody)
	require.Equal(t, http.StatusNotFound, status, body)
	require.Contains(t, body, "async image tasks are not enabled")

	e.world.sel.deps.ImageTasks = service.NewImageTaskServiceWithUploader(&memTaskStore{}, nil, time.Hour, time.Minute)
	// 从节点缓存着功能状态（30 秒）：换一个新的从节点客户端才看得到刚开的状态，这里直接等缓存过期不现实，所以重新起一套。
	e2 := startStandardE2E(t, func(upstream string) []service.Account {
		accounts[0].Credentials["base_url"] = upstream
		return accounts
	})
	e2.world.keys.keys["sk-a"].Group.AllowImageGeneration = true
	e2.world.sel.deps.ImageTasks = e.world.sel.deps.ImageTasks

	status, body = e2.post(t, "/v1/images/generations/async", "sk-a", imagesBody)
	require.Equal(t, http.StatusAccepted, status, body)
	id := gjson.Get(body, "id").String()
	require.True(t, strings.HasPrefix(id, "imgtask_"), body)
	require.Equal(t, "/v1/images/tasks/"+id, gjson.Get(body, "poll_url").String())

	var polled string
	require.Eventually(t, func() bool {
		status, polled = e2.get(t, "/v1/images/tasks/"+id, "sk-a")
		return status == http.StatusOK && gjson.Get(polled, "status").String() == service.ImageTaskStatusCompleted
	}, 10*time.Second, 50*time.Millisecond, polled)
	require.NotEmpty(t, gjson.Get(polled, "result.data.0.b64_json").String(), "the generated image came back through the master")

	// 执行走同步图片入口：选号、扣费记录照常（一条 OpenAI 记录）。
	require.Eventually(t, func() bool { return len(e2.settler.records()) == 1 }, 5*time.Second, 20*time.Millisecond)
	require.Equal(t, relayv1.UsageRecordKind_USAGE_RECORD_KIND_OPENAI, e2.settler.records()[0].GetKind())

	// 别的 Key（同一个用户）查不到这个任务。
	status, body = e2.get(t, "/v1/images/tasks/"+id, "sk-b")
	require.Equal(t, http.StatusNotFound, status, body)
}
