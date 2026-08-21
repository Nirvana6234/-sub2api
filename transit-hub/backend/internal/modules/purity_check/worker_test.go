package purity_check

import (
	"context"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"transithub/backend/internal/modules/upstream"
)

// ---- 假实现 ----

type fakeRepo struct {
	mu       sync.Mutex
	jobs     map[string]*Job
	order    []string
	reports  map[string][]byte
	progress []int
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{jobs: map[string]*Job{}, reports: map[string][]byte{}}
}

func (r *fakeRepo) InsertJobs(_ context.Context, jobs []Job) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range jobs {
		job := jobs[i]
		r.jobs[job.ID] = &job
		r.order = append(r.order, job.ID)
	}
	return nil
}

func (r *fakeRepo) ListJobs(context.Context, string, string, int) ([]Job, error) { return nil, nil }
func (r *fakeRepo) GetJob(_ context.Context, id string, _ string, _ string) (*Job, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	job, ok := r.jobs[id]
	if !ok {
		return nil, nil
	}
	copied := *job
	return &copied, nil
}

func (r *fakeRepo) ClaimNextQueuedJob(context.Context) (*Job, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, id := range r.order {
		job := r.jobs[id]
		if job.Status == StatusQueued {
			job.Status = StatusRunning
			copied := *job
			return &copied, nil
		}
	}
	return nil, nil
}

func (r *fakeRepo) ReleaseJob(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if job, ok := r.jobs[id]; ok && job.Status == StatusRunning {
		job.Status = StatusQueued
	}
	return nil
}

func (r *fakeRepo) SetDetectorSession(_ context.Context, id string, sessionID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if job, ok := r.jobs[id]; ok {
		job.DetectorSessionID = sessionID
	}
	return nil
}

func (r *fakeRepo) UpdateProgress(_ context.Context, id string, planned, completed, failed int) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.progress = append(r.progress, completed)
	if job, ok := r.jobs[id]; ok {
		job.PlannedRequests, job.CompletedRequests, job.FailedRequests = planned, completed, failed
	}
	return nil
}

func (r *fakeRepo) FinishJob(_ context.Context, id string, status Status, key string, detail string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if job, ok := r.jobs[id]; ok {
		job.Status, job.ErrorKey, job.ErrorDetail = status, key, detail
	}
	return nil
}

func (r *fakeRepo) CancelQueuedJob(context.Context, string, string, string) (bool, error) {
	return false, nil
}
func (r *fakeRepo) ResetStaleRunningJobs(context.Context) (int64, error) { return 0, nil }

func (r *fakeRepo) SaveReport(_ context.Context, jobID string, payload []byte, _ Report) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reports[jobID] = payload
	return nil
}

func (r *fakeRepo) GetReport(context.Context, string) (*Report, error) { return nil, nil }
func (r *fakeRepo) ListReportSummaries(context.Context, []string) (map[string]Report, error) {
	return map[string]Report{}, nil
}
func (r *fakeRepo) CountQueued(context.Context) (int, error) { return 0, nil }

func (r *fakeRepo) DeleteJob(_ context.Context, id, userID, adminAccountID string) (string, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	job, ok := r.jobs[id]
	if !ok || job.UserID != userID || job.AdminAccountID != adminAccountID {
		return "", false, nil
	}
	switch job.Status {
	case StatusSucceeded, StatusFailed, StatusCancelled:
	default:
		return "", false, nil
	}
	delete(r.jobs, id)
	delete(r.reports, id)
	for i, oid := range r.order {
		if oid == id {
			r.order = append(r.order[:i], r.order[i+1:]...)
			break
		}
	}
	return job.DetectorSessionID, true, nil
}

func (r *fakeRepo) PruneJobs(_ context.Context, userID, adminAccountID string, keep int) ([]string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	// 只有终态任务参与配额，按创建时间倒序保留最新的 keep 条。
	var terminal []*Job
	for _, id := range r.order {
		job := r.jobs[id]
		if job.UserID != userID || job.AdminAccountID != adminAccountID {
			continue
		}
		switch job.Status {
		case StatusSucceeded, StatusFailed, StatusCancelled:
			terminal = append(terminal, job)
		}
	}
	sort.Slice(terminal, func(i, j int) bool { return terminal[i].CreatedAt.After(terminal[j].CreatedAt) })
	if len(terminal) <= keep {
		return nil, nil
	}
	sessions := make([]string, 0)
	for _, job := range terminal[keep:] {
		sessions = append(sessions, job.DetectorSessionID)
		delete(r.jobs, job.ID)
		delete(r.reports, job.ID)
		for i, oid := range r.order {
			if oid == job.ID {
				r.order = append(r.order[:i], r.order[i+1:]...)
				break
			}
		}
	}
	return sessions, nil
}

func (r *fakeRepo) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.jobs)
}

func (r *fakeRepo) status(id string) Status {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.jobs[id].Status
}

type fakeSessions struct{}

func (fakeSessions) RequireSession(context.Context, string, string) (upstream.Session, error) {
	return upstream.Session{Platform: upstream.PlatformSub2API, BaseURL: "https://sub2api.example"}, nil
}

type fakeReader struct {
	key         string
	resolveCall int32
}

func (f *fakeReader) ListAdminAllAccounts(upstream.Session) ([]upstream.AdminGroupAccountInfo, error) {
	return []upstream.AdminGroupAccountInfo{
		{ID: "acc-1", Name: "relay-a", Platform: "openai", Type: "apikey"},
		{ID: "acc-2", Name: "claude-b", Platform: "anthropic", Type: "apikey"},
		{ID: "acc-3", Name: "codex-oauth", Platform: "openai", Type: "oauth"},
		// 早期接入的中转账号是这个遗留类型，同样带 base_url + 静态 key。
		{ID: "acc-4", Name: "relay-legacy", Platform: "openai", Type: "upstream"},
		{ID: "acc-5", Name: "bedrock-x", Platform: "openai", Type: "bedrock"},
	}, nil
}

func (f *fakeReader) ResolveProbeCredential(_ upstream.Session, account upstream.AdminGroupAccountInfo) (upstream.ProbeCredential, error) {
	atomic.AddInt32(&f.resolveCall, 1)
	return upstream.ProbeCredential{BaseURL: "https://relay-a.example/v1", Key: f.key}, nil
}

func newTestService(repo purityRepository, reader AccountReader, detector *DetectorClient) *Service {
	service := &Service{repo: repo, sessions: fakeSessions{}, reader: reader, detector: detector}
	service.worker = newWorker(service)
	return service
}

// ---- 测试 ----

// TestWorkerRunsJobToCompletion 走完整条链路：领任务 → 取凭据 → start →
// 轮询进度 → 拉报告 → 落终态，并确认明文 key 确实发给了检测器、
// 而落库的报告里没有它。
func TestWorkerRunsJobToCompletion(t *testing.T) {
	const secretKey = "sk-super-secret-value"
	var sawKey atomic.Bool
	var polls atomic.Int32

	detector := newFakeDetector(t, fakeDetectorBehaviour{
		onStart: func(req map[string]any) {
			if req["api_key"] == secretKey {
				sawKey.Store(true)
			}
		},
		statusFor: func() string {
			// 前两次报 running（带进度），之后完成。
			if polls.Add(1) <= 2 {
				return `{"status":"running","session_id":"s1","report_available":false,
					"progress":{"planned":19,"logical_completed":8,"errors":1}}`
			}
			return `{"status":"complete","session_id":"s1","report_available":true,
				"progress":{"planned":19,"logical_completed":19,"errors":1}}`
		},
		report: `{"overall_verdict":"通过","official":true,"fingerprint_model":"gpt-5.6-sol",
			"fingerprint_claim_mismatch":false,"auth_values_persisted":false}`,
	})
	defer detector.Close()

	repo := newFakeRepo()
	reader := &fakeReader{key: secretKey}
	service := newTestService(repo, reader, NewDetectorClient(detector.URL))

	job := Job{ID: "job-1", UserID: "u1", AccountID: "acc-1", Tier: TierLow,
		ClaimedModel: ModelSol, RequestModel: ModelSol, Status: StatusQueued}
	_ = repo.InsertJobs(context.Background(), []Job{job})

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := service.worker.runNext(ctx); err != nil {
		t.Fatalf("runNext: %v", err)
	}

	if got := repo.status("job-1"); got != StatusSucceeded {
		t.Fatalf("期望 succeeded，实际 %s", got)
	}
	if !sawKey.Load() {
		t.Error("检测器没收到明文 key，检测无法进行")
	}
	if len(repo.progress) == 0 {
		t.Error("运行中应该写过进度，否则前端进度条永远是 0")
	}
	stored := string(repo.reports["job-1"])
	if stored == "" {
		t.Fatal("报告没落库")
	}
	if strings.Contains(stored, secretKey) {
		t.Error("落库的报告里出现了明文 key")
	}
}

// TestWorkerRequeuesWhenDetectorBusy 覆盖多副本争抢：检测器已被别人占用时，
// 任务必须原位放回队列等下一轮，而不是被判失败。判失败等于用户白提交一次。
func TestWorkerRequeuesWhenDetectorBusy(t *testing.T) {
	detector := newFakeDetector(t, fakeDetectorBehaviour{busy: true})
	defer detector.Close()

	repo := newFakeRepo()
	service := newTestService(repo, &fakeReader{key: "sk"}, NewDetectorClient(detector.URL))
	_ = repo.InsertJobs(context.Background(), []Job{{
		ID: "job-busy", UserID: "u1", AccountID: "acc-1", Tier: TierLow,
		ClaimedModel: ModelSol, RequestModel: ModelSol, Status: StatusQueued,
	}})

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if _, err := service.worker.runNext(ctx); err != nil {
		t.Fatalf("runNext: %v", err)
	}

	if got := repo.status("job-busy"); got != StatusQueued {
		t.Fatalf("检测器忙时任务应回到 queued，实际 %s", got)
	}
}

// TestWorkerFailsOnDetectorError 确认检测器报错（非 busy）时任务落成 failed，
// 不会无限重排队把队首堵死。
func TestWorkerFailsOnDetectorError(t *testing.T) {
	detector := newFakeDetector(t, fakeDetectorBehaviour{startError: `{"error":"invalid api base url"}`})
	defer detector.Close()

	repo := newFakeRepo()
	service := newTestService(repo, &fakeReader{key: "sk"}, NewDetectorClient(detector.URL))
	_ = repo.InsertJobs(context.Background(), []Job{{
		ID: "job-err", UserID: "u1", AccountID: "acc-1", Tier: TierLow,
		ClaimedModel: ModelSol, RequestModel: ModelSol, Status: StatusQueued,
	}})

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if _, err := service.worker.runNext(ctx); err != nil {
		t.Fatalf("runNext: %v", err)
	}
	if got := repo.status("job-err"); got != StatusFailed {
		t.Fatalf("检测器报错时应落 failed，实际 %s", got)
	}
}

// TestWorkerMarksStoppedAsCancelled 确认用户中途取消后，任务如实记成 cancelled
// 而不是 succeeded——一个被打断的样本不该被当成一次有效检测。
func TestWorkerMarksStoppedAsCancelled(t *testing.T) {
	detector := newFakeDetector(t, fakeDetectorBehaviour{
		statusFor: func() string {
			return `{"status":"stopped","session_id":"s1","report_available":true,
				"progress":{"planned":19,"logical_completed":5,"errors":0}}`
		},
		report: `{"overall_verdict":"证据不足","official":true,"run_stopped":true}`,
	})
	defer detector.Close()

	repo := newFakeRepo()
	service := newTestService(repo, &fakeReader{key: "sk"}, NewDetectorClient(detector.URL))
	_ = repo.InsertJobs(context.Background(), []Job{{
		ID: "job-stop", UserID: "u1", AccountID: "acc-1", Tier: TierLow,
		ClaimedModel: ModelSol, RequestModel: ModelSol, Status: StatusQueued,
	}})

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if _, err := service.worker.runNext(ctx); err != nil {
		t.Fatalf("runNext: %v", err)
	}
	if got := repo.status("job-stop"); got != StatusCancelled {
		t.Fatalf("被停止的检测应记成 cancelled，实际 %s", got)
	}
	if len(repo.reports["job-stop"]) == 0 {
		t.Error("即使被中断，已产出的报告仍应保留")
	}
}

// TestListTargetsFiltersIneligible 确认目标筛选：只有 openai + apikey 能测。
// anthropic 账号跑这套探针没意义；openai 的 oauth 账号会被解析到 chatgpt.com
// 的 codex 后端，请求形状对不上。
func TestListTargetsFiltersIneligible(t *testing.T) {
	repo := newFakeRepo()
	service := newTestService(repo, &fakeReader{key: "sk"}, NewDetectorClient("http://unused"))
	service.accounts = fakeAdminAccounts{}

	targets, err := service.ListTargets(context.Background(), "u1")
	if err != nil {
		t.Fatalf("ListTargets: %v", err)
	}
	if len(targets) != 5 {
		t.Fatalf("应返回全部 5 个账号（不合格的置灰而不是隐藏），实际 %d", len(targets))
	}

	byID := map[string]Target{}
	for _, target := range targets {
		byID[target.AccountID] = target
	}
	if !byID["acc-1"].Eligible {
		t.Error("openai + apikey 应可检测")
	}
	// 这条是防回归的重点：sub2api 早期接入的中转账号 type 是 upstream 而不是
	// apikey，只认 apikey 会把接得最早的那批中转从列表里整个漏掉。
	if !byID["acc-4"].Eligible {
		t.Errorf("openai + upstream（遗留中转账号类型）应可检测，实际 reason=%q", byID["acc-4"].Reason)
	}
	if byID["acc-2"].Eligible || byID["acc-2"].Reason != ReasonNotOpenAI {
		t.Errorf("anthropic 账号应因非 openai 被排除，实际 eligible=%v reason=%q",
			byID["acc-2"].Eligible, byID["acc-2"].Reason)
	}
	if byID["acc-3"].Eligible || byID["acc-3"].Reason != ReasonNotAPIKey {
		t.Errorf("openai oauth 账号应被排除（走 chatgpt.com codex 后端），实际 eligible=%v reason=%q",
			byID["acc-3"].Eligible, byID["acc-3"].Reason)
	}
	if byID["acc-5"].Eligible {
		t.Error("bedrock 账号没有可直接用的 Bearer key，应被排除")
	}
}

// TestSubmitRejectsIneligibleTarget 确认后端也拦一道：前端置了灰不代表
// 请求不会被构造出来。
func TestSubmitRejectsIneligibleTarget(t *testing.T) {
	repo := newFakeRepo()
	detector := newFakeDetector(t, fakeDetectorBehaviour{})
	defer detector.Close()
	service := newTestService(repo, &fakeReader{key: "sk"}, NewDetectorClient(detector.URL))
	service.accounts = fakeAdminAccounts{}

	_, err := service.Submit(context.Background(), "u1", SubmitInput{
		AccountIDs: []string{"acc-2"}, Tier: TierLow, ClaimedModel: ModelSol,
	})
	if err == nil || !strings.Contains(err.Error(), ErrorTargetIneligible) {
		t.Fatalf("提交 anthropic 账号应被拒，实际 %v", err)
	}
}

// TestSubmitRejectsUnknownClaimedModel 确认申报型号被限制在三个已标定的型号内。
func TestSubmitRejectsUnknownClaimedModel(t *testing.T) {
	repo := newFakeRepo()
	service := newTestService(repo, &fakeReader{key: "sk"}, NewDetectorClient("http://x"))
	service.accounts = fakeAdminAccounts{}

	_, err := service.Submit(context.Background(), "u1", SubmitInput{
		AccountIDs: []string{"acc-1"}, Tier: TierLow, ClaimedModel: "gpt-4o",
	})
	if err == nil || !strings.Contains(err.Error(), ErrorInvalidModel) {
		t.Fatalf("未标定的型号应被拒，实际 %v", err)
	}
}

type fakeAdminAccounts struct{}

func (fakeAdminAccounts) RequireCurrentID(context.Context, string) (string, error) {
	return "workspace-1", nil
}
