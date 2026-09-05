package purity_check

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func seedTerminalJobs(t *testing.T, repo *fakeRepo, n int) {
	t.Helper()
	base := time.Now().Add(-time.Duration(n) * time.Minute)
	jobs := make([]Job, 0, n)
	for i := 0; i < n; i++ {
		jobs = append(jobs, Job{
			ID:                "job-" + strconv.Itoa(i),
			UserID:            "u1",
			AdminAccountID:    "workspace-1",
			AccountID:         "acc-1",
			Status:            StatusSucceeded,
			DetectorSessionID: "sess" + strconv.Itoa(i),
			CreatedAt:         base.Add(time.Duration(i) * time.Minute),
		})
	}
	if err := repo.InsertJobs(context.Background(), jobs); err != nil {
		t.Fatalf("InsertJobs: %v", err)
	}
	// InsertJobs 把状态写死成 queued，这里改回终态：本用例要的是「历史」。
	repo.mu.Lock()
	for _, j := range jobs {
		repo.jobs[j.ID].Status = StatusSucceeded
	}
	repo.mu.Unlock()
}

// TestPruneKeepsAtMostMaxRetained 确认历史被裁到上限，且删的是最旧的那批。
func TestPruneKeepsAtMostMaxRetained(t *testing.T) {
	repo := newFakeRepo()
	seedTerminalJobs(t, repo, MaxRetainedJobs+15)

	service := newTestService(repo, &fakeReader{key: "sk"}, NewDetectorClient("http://unused"))
	service.pruneHistory(context.Background(), "u1", "workspace-1")

	if got := repo.count(); got != MaxRetainedJobs {
		t.Fatalf("裁剪后应剩 %d 条，实际 %d", MaxRetainedJobs, got)
	}
	// job-0 最旧，必须被删；最新的 job-114 必须还在。
	repo.mu.Lock()
	_, oldestAlive := repo.jobs["job-0"]
	_, newestAlive := repo.jobs["job-"+strconv.Itoa(MaxRetainedJobs+14)]
	repo.mu.Unlock()
	if oldestAlive {
		t.Error("最旧的任务应被裁掉")
	}
	if !newestAlive {
		t.Error("最新的任务不该被裁掉")
	}
}

// TestPruneNeverTouchesActiveJobs 是这条约束里最容易出事的地方：
// 一次批量提交 30 个时它们都还是 queued，如果把它们算进保留配额，
// 用户刚排的队会被自己的历史挤掉。
func TestPruneNeverTouchesActiveJobs(t *testing.T) {
	repo := newFakeRepo()
	seedTerminalJobs(t, repo, MaxRetainedJobs)

	active := []Job{
		{ID: "queued-1", UserID: "u1", AdminAccountID: "workspace-1", Status: StatusQueued,
			CreatedAt: time.Now().Add(-time.Hour)},
		{ID: "running-1", UserID: "u1", AdminAccountID: "workspace-1", Status: StatusRunning,
			CreatedAt: time.Now().Add(-2 * time.Hour)},
	}
	_ = repo.InsertJobs(context.Background(), active)
	repo.mu.Lock()
	repo.jobs["running-1"].Status = StatusRunning
	repo.mu.Unlock()

	service := newTestService(repo, &fakeReader{key: "sk"}, NewDetectorClient("http://unused"))
	service.pruneHistory(context.Background(), "u1", "workspace-1")

	repo.mu.Lock()
	_, queuedAlive := repo.jobs["queued-1"]
	_, runningAlive := repo.jobs["running-1"]
	repo.mu.Unlock()
	if !queuedAlive {
		t.Error("排队中的任务被裁掉了——用户刚提交的检测会凭空消失")
	}
	if !runningAlive {
		t.Error("运行中的任务被裁掉了——worker 会写到一个不存在的 job 上")
	}
}

// TestPruneIsPerWorkspace 确认配额按 workspace 独立计算，
// 一个 workspace 跑满 100 不该把另一个的历史删掉。
func TestPruneIsPerWorkspace(t *testing.T) {
	repo := newFakeRepo()
	seedTerminalJobs(t, repo, MaxRetainedJobs+5)
	other := []Job{{ID: "other-1", UserID: "u2", AdminAccountID: "workspace-2",
		Status: StatusSucceeded, CreatedAt: time.Now().Add(-99 * time.Hour)}}
	_ = repo.InsertJobs(context.Background(), other)
	repo.mu.Lock()
	repo.jobs["other-1"].Status = StatusSucceeded
	repo.mu.Unlock()

	service := newTestService(repo, &fakeReader{key: "sk"}, NewDetectorClient("http://unused"))
	service.pruneHistory(context.Background(), "u1", "workspace-1")

	repo.mu.Lock()
	_, otherAlive := repo.jobs["other-1"]
	repo.mu.Unlock()
	if !otherAlive {
		t.Error("裁剪串到了别的 workspace")
	}
}

// TestPruneRemovesDetectorRunDirs 确认裁掉任务时，检测器旁路服务那边对应会话的
// 落盘目录也被删了——否则数据库裁住了，那个卷还是无限涨。
func TestPruneRemovesDetectorRunDirs(t *testing.T) {
	runsDir := t.TempDir()
	repo := newFakeRepo()
	seedTerminalJobs(t, repo, MaxRetainedJobs+3)

	// 给每条任务造一个会话目录。
	repo.mu.Lock()
	ids := make([]string, 0, len(repo.jobs))
	for _, job := range repo.jobs {
		ids = append(ids, job.DetectorSessionID)
	}
	repo.mu.Unlock()
	for _, sid := range ids {
		dir := filepath.Join(runsDir, "detector", "session-"+sid)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "state.sqlite3"), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	service := newTestService(repo, &fakeReader{key: "sk"}, NewDetectorClient("http://unused"))
	service.detectorRunsDir = runsDir
	service.pruneHistory(context.Background(), "u1", "workspace-1")

	// 最旧的 3 个被裁，目录应该跟着没了。
	for i := 0; i < 3; i++ {
		dir := filepath.Join(runsDir, "detector", "session-sess"+strconv.Itoa(i))
		if _, err := os.Stat(dir); !os.IsNotExist(err) {
			t.Errorf("被裁任务的检测器目录还在: %s", dir)
		}
	}
	// 保留下来的不能动。
	keep := filepath.Join(runsDir, "detector", "session-sess"+strconv.Itoa(MaxRetainedJobs+2))
	if _, err := os.Stat(keep); err != nil {
		t.Errorf("保留任务的检测器目录被误删: %v", err)
	}
}

// TestRemoveDetectorRunRejectsTraversal 防路径穿越：sessionID 会被拼进路径，
// 万一检测器哪天改了 id 格式，不能让 ".." 把删除操作带出目录。
func TestRemoveDetectorRunRejectsTraversal(t *testing.T) {
	runsDir := t.TempDir()
	victim := filepath.Join(runsDir, "important.txt")
	if err := os.WriteFile(victim, []byte("keep me"), 0o644); err != nil {
		t.Fatal(err)
	}

	service := newTestService(newFakeRepo(), &fakeReader{key: "sk"}, NewDetectorClient("http://unused"))
	service.detectorRunsDir = runsDir
	for _, evil := range []string{"../..", "../../important.txt", "a/../../b", "x`rm -rf /`"} {
		service.removeDetectorRun(evil)
	}
	if _, err := os.Stat(victim); err != nil {
		t.Fatalf("目录外的文件被删了: %v", err)
	}
}

// TestDeleteJobOnlyTerminal 确认删除按钮只能删已结束的任务；
// 排队中/运行中的必须先取消，否则 worker 会拿着一个不存在的 job 继续跑。
func TestDeleteJobOnlyTerminal(t *testing.T) {
	repo := newFakeRepo()
	_ = repo.InsertJobs(context.Background(), []Job{
		{ID: "done", UserID: "u1", AdminAccountID: "workspace-1", Status: StatusSucceeded,
			DetectorSessionID: "sessdone", CreatedAt: time.Now()},
		{ID: "busy", UserID: "u1", AdminAccountID: "workspace-1", Status: StatusRunning,
			CreatedAt: time.Now()},
	})
	repo.mu.Lock()
	repo.jobs["done"].Status = StatusSucceeded
	repo.jobs["busy"].Status = StatusRunning
	repo.mu.Unlock()

	service := newTestService(repo, &fakeReader{key: "sk"}, NewDetectorClient("http://unused"))
	service.accounts = fakeAdminAccounts{}

	if err := service.Delete(context.Background(), "u1", "done"); err != nil {
		t.Fatalf("删除已完成任务应成功，实际 %v", err)
	}
	err := service.Delete(context.Background(), "u1", "busy")
	if err == nil || !strings.Contains(err.Error(), ErrorNotDeletable) {
		t.Fatalf("删除运行中任务应报 notDeletable，实际 %v", err)
	}
	if err := service.Delete(context.Background(), "u1", "nope"); err == nil ||
		!strings.Contains(err.Error(), ErrorNotFound) {
		t.Fatalf("删除不存在的任务应报 notFound，实际 %v", err)
	}
}
