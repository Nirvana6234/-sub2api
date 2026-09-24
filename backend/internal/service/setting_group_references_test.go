package service

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

// groupRefSettingRepo 是一个可读写的 settings 内存实现，用于验证清理确实落库。
type groupRefSettingRepo struct {
	mu       sync.Mutex
	values   map[string]string
	setCalls int
}

func newGroupRefSettingRepo(values map[string]string) *groupRefSettingRepo {
	copied := make(map[string]string, len(values))
	for k, v := range values {
		copied[k] = v
	}
	return &groupRefSettingRepo{values: copied}
}

func (r *groupRefSettingRepo) Get(context.Context, string) (*Setting, error) { return nil, nil }

func (r *groupRefSettingRepo) GetValue(_ context.Context, key string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.values[key], nil
}

func (r *groupRefSettingRepo) Set(_ context.Context, key, value string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.values[key] = value
	return nil
}

func (r *groupRefSettingRepo) GetMultiple(_ context.Context, keys []string) (map[string]string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make(map[string]string, len(keys))
	for _, key := range keys {
		if value, ok := r.values[key]; ok {
			out[key] = value
		}
	}
	return out, nil
}

func (r *groupRefSettingRepo) SetMultiple(_ context.Context, settings map[string]string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.setCalls++
	for key, value := range settings {
		r.values[key] = value
	}
	return nil
}

func (r *groupRefSettingRepo) GetAll(context.Context) (map[string]string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make(map[string]string, len(r.values))
	for k, v := range r.values {
		out[k] = v
	}
	return out, nil
}

func (r *groupRefSettingRepo) Delete(context.Context, string) error { return nil }

func (r *groupRefSettingRepo) value(key string) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.values[key]
}

// 分组删除后必须从 playground 候选列表里消失。留下悬挂 ID 的后果不是"这一项
// 配置失效"，而是整个系统设置页面再也保存不了任何东西——保存走整文档校验，
// 一个已删除分组会让邀请返利这类无关设置一起报
// "playground group N does not exist"，而管理台只渲染活跃分组，用户在界面上
// 看不到也删不掉它。
func TestRemoveGroupReferencesDropsDeletedGroup(t *testing.T) {
	repo := newGroupRefSettingRepo(map[string]string{
		SettingKeyPlaygroundDefaultChatGroupIDs:  "[2,10,11]",
		SettingKeyPlaygroundDefaultImageGroupIDs: "[13]",
	})
	svc := &SettingService{settingRepo: repo}

	require.NoError(t, svc.RemoveGroupReferences(context.Background(), 10))

	require.Equal(t, "[2,11]", repo.value(SettingKeyPlaygroundDefaultChatGroupIDs),
		"已删除分组必须被剔除，且保留其余分组的原有顺序")
	require.Equal(t, "[13]", repo.value(SettingKeyPlaygroundDefaultImageGroupIDs),
		"未引用该分组的设置不得被改写")
}

func TestRemoveGroupReferencesIsNoOpWhenUnreferenced(t *testing.T) {
	repo := newGroupRefSettingRepo(map[string]string{
		SettingKeyPlaygroundDefaultChatGroupIDs:  "[2,11]",
		SettingKeyPlaygroundDefaultImageGroupIDs: "[13]",
	})
	svc := &SettingService{settingRepo: repo}

	require.NoError(t, svc.RemoveGroupReferences(context.Background(), 10))

	require.Zero(t, repo.setCalls, "没有引用该分组时不应该写库")
	require.Equal(t, "[2,11]", repo.value(SettingKeyPlaygroundDefaultChatGroupIDs))
}

func TestRemoveGroupReferencesClearsLastEntry(t *testing.T) {
	// 清空后必须是合法的空数组，不能写成 null——parsePlaygroundGroupIDs 对
	// 非法 JSON 会静默返回空列表，那会掩盖写坏数据的问题。
	repo := newGroupRefSettingRepo(map[string]string{
		SettingKeyPlaygroundDefaultChatGroupIDs: "[10]",
	})
	svc := &SettingService{settingRepo: repo}

	require.NoError(t, svc.RemoveGroupReferences(context.Background(), 10))
	require.Equal(t, "[]", repo.value(SettingKeyPlaygroundDefaultChatGroupIDs))
}

func TestRemoveGroupReferencesIgnoresInvalidInput(t *testing.T) {
	repo := newGroupRefSettingRepo(map[string]string{
		SettingKeyPlaygroundDefaultChatGroupIDs: "[2,11]",
	})
	svc := &SettingService{settingRepo: repo}

	require.NoError(t, svc.RemoveGroupReferences(context.Background(), 0))
	require.Zero(t, repo.setCalls, "非法分组 ID 不应触发任何写入")

	var nilSvc *SettingService
	require.NoError(t, nilSvc.RemoveGroupReferences(context.Background(), 10))
}
