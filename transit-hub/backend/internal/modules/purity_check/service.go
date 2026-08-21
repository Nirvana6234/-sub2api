package purity_check

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"strings"

	"transithub/backend/internal/modules/upstream"
)

// purityRepository 是 Service 对存储层的全部依赖，由 *Repository 结构性满足。
// 定义成接口是为了让排队/结算逻辑能用内存假实现单测，跟 connection_health 一致。
type purityRepository interface {
	InsertJobs(ctx context.Context, jobs []Job) error
	ListJobs(ctx context.Context, userID string, adminAccountID string, limit int) ([]Job, error)
	GetJob(ctx context.Context, id string, userID string, adminAccountID string) (*Job, error)
	ClaimNextQueuedJob(ctx context.Context) (*Job, error)
	ReleaseJob(ctx context.Context, id string) error
	SetDetectorSession(ctx context.Context, id string, sessionID string) error
	UpdateProgress(ctx context.Context, id string, planned int, completed int, failed int) error
	FinishJob(ctx context.Context, id string, status Status, errorKey string, errorDetail string) error
	CancelQueuedJob(ctx context.Context, id string, userID string, adminAccountID string) (bool, error)
	ResetStaleRunningJobs(ctx context.Context) (int64, error)
	SaveReport(ctx context.Context, jobID string, payload []byte, summary Report) error
	GetReport(ctx context.Context, jobID string) (*Report, error)
	ListReportSummaries(ctx context.Context, jobIDs []string) (map[string]Report, error)
	CountQueued(ctx context.Context) (int, error)
	DeleteJob(ctx context.Context, id string, userID string, adminAccountID string) (string, bool, error)
	PruneJobs(ctx context.Context, userID string, adminAccountID string, keep int) ([]string, error)
}

// MaxRetainedJobs 是每个 workspace 保留的检测历史条数上限。
//
// 超出后按创建时间从旧到新删。只裁终态任务：排队中/运行中的不占配额也不会被删，
// 否则一次批量提交 30 个就会把自己刚排进去的任务削掉。
//
// 单份报告 13–31 kB（高档最大），100 份约 3 MB；检测器旁路服务那边每个会话
// 140–432 kB，100 个约 20 MB——都由这个上限一起兜住。
const MaxRetainedJobs = 100

type Service struct {
	repo     purityRepository
	sessions SessionProvider
	accounts AdminAccountResolver
	reader   AccountReader
	detector *DetectorClient

	// detectorRunsDir 是检测器旁路服务的落盘根目录在本容器内的挂载路径。
	// 为空表示没挂——那就只清数据库，不动文件，功能照常。
	detectorRunsDir string

	worker *worker
}

func NewService(repo *Repository, sessions SessionProvider, reader AccountReader, detector *DetectorClient, detectorRunsDir string) *Service {
	service := &Service{
		repo:            repo,
		sessions:        sessions,
		reader:          reader,
		detector:        detector,
		detectorRunsDir: strings.TrimSpace(detectorRunsDir),
	}
	service.worker = newWorker(service)
	return service
}

func (s *Service) SetAdminAccountResolver(accounts AdminAccountResolver) {
	s.accounts = accounts
}

func (s *Service) currentAdminAccountID(ctx context.Context, userID string) (string, error) {
	if s.accounts == nil {
		return "", requestError(ErrorNoCurrentAccount)
	}
	return s.accounts.RequireCurrentID(ctx, userID)
}

// DetectorConfigured 让 handler 在检测器没部署时给出明确提示，而不是每个接口
// 各自超时。
func (s *Service) DetectorConfigured() bool { return s.detector.Configured() }

// ---- 目标列表 ----

// ListTargets 返回当前 workspace 下所有上游账号，并标注哪些能测。
//
// 能测的条件是 platform=openai 且账号自带「base_url + 静态 api_key」：
//   - 非 openai 平台（anthropic/gemini/...）：检测器的指纹基线只为 GPT-5.6 的
//     Sol/Terra/Luna 标定过，对别的模型跑没有意义；
//   - openai 但 type 是 oauth/setup-token：这类账号的 base_url 会被解析成
//     chatgpt.com 的 codex 后端，请求形状跟标准 /v1/responses 不一样，
//     检测器打不通（见 upstream/probe_credentials.go 的 isOpenAIOAuthProbeAccount）；
//   - bedrock / service_account 走的是 SigV4 / Google 凭据，没有可直接用的 Bearer key。
func (s *Service) ListTargets(ctx context.Context, userID string) ([]Target, error) {
	session, err := s.requireSession(ctx, userID)
	if err != nil {
		return nil, err
	}
	accounts, err := s.reader.ListAdminAllAccounts(session)
	if err != nil {
		return nil, requestError(ErrorAccountsFetch)
	}

	targets := make([]Target, 0, len(accounts))
	for _, account := range accounts {
		target := Target{
			AccountID: account.ID,
			Name:      account.Name,
			Platform:  account.Platform,
			Type:      account.Type,
			BaseURL:   account.BaseURL,
			GroupIDs:  account.GroupIDs,
		}
		switch {
		case !isOpenAIPlatform(account.Platform):
			target.Reason = ReasonNotOpenAI
		case !isStaticKeyType(account.Type):
			target.Reason = ReasonNotAPIKey
		default:
			target.Eligible = true
		}
		targets = append(targets, target)
	}
	return targets, nil
}

func isOpenAIPlatform(platform string) bool {
	return strings.EqualFold(strings.TrimSpace(platform), "openai")
}

// isStaticKeyType 判断账号是否自带「base_url + 静态 api_key」，也就是检测器
// 能直接拿去打 OpenAI 兼容接口的那种。
//
// 除了 apikey，还必须收 upstream：sub2api 的 AccountTypeUpstream 是第三方中转
// 账号的**遗留**类型（现在管理台新建走 apikey，但老行还是 upstream），它同样
// 带 base_url 和静态 key（见 sub2api 的 Account.GetOpenAIBaseURL，两种类型
// 走的是同一个分支）。漏掉它会让接得早的那批中转账号从列表里凭空消失。
//
// 反过来 oauth / setup-token / bedrock / service_account 一律排除：
// 它们要么没有可直接用的 Bearer key，要么端点形状对不上。
func isStaticKeyType(accountType string) bool {
	switch strings.ToLower(strings.TrimSpace(accountType)) {
	case "apikey", "api_key", "upstream":
		return true
	}
	return false
}

// ---- 档位信息 ----

// TierInfos 从检测器现取三个档位的预估，不在 Go 侧硬编码请求数。
// 档位定义属于检测器，写死在这边迟早跟它对不上。
func (s *Service) TierInfos(ctx context.Context) ([]TierInfo, error) {
	if !s.detector.Configured() {
		return nil, requestError(ErrorDetectorUnavailable)
	}
	tiers := []Tier{TierLow, TierMedium, TierHigh}
	out := make([]TierInfo, 0, len(tiers))
	for _, tier := range tiers {
		estimate, err := s.detector.Estimate(ctx, tier)
		if err != nil {
			return nil, requestError(ErrorDetectorUnavailable)
		}
		out = append(out, TierInfo{
			Tier:          tier,
			TotalRequests: estimate.TotalRequests,
			// 官方预设 retries=2，一条逻辑任务最坏会打 3 次 HTTP。
			// 实测：19 条全失败时发了 57 次。UI 上必须把这个上限讲清楚，
			// 否则用户以为低档只花 19 次请求的钱。
			MaxHTTPRequests:        estimate.TotalRequests * (officialRetries + 1),
			ApproximateInputTokens: estimate.ApproximateInputTokens,
			Fixed32KRequests:       estimate.Fixed32KRequests,
			EstimateDisclaimer:     estimate.EstimateDisclaimerCN,
		})
	}
	return out, nil
}

// officialRetries 是三个官方 single 预设共有的 retries 值。
// 【不要为了少打请求去调它】：配置哈希一变就不是官方档，结论会从
// 「强烈指向」降级成参考值。这里只是拿来算最坏请求数。
const officialRetries = 2

// ---- 提交 ----

// Submit 把一批账号排进检测队列。凭据在这里不解析——排队可能要等很久，
// 提前取出来的明文 key 存在内存里等几小时既没必要也不安全。
// worker 真正要启动某个任务时才现取。
func (s *Service) Submit(ctx context.Context, userID string, input SubmitInput) ([]Job, error) {
	if !s.detector.Configured() {
		return nil, requestError(ErrorDetectorUnavailable)
	}
	if !input.Tier.valid() {
		return nil, requestError(ErrorInvalidTier)
	}
	claimed := strings.TrimSpace(input.ClaimedModel)
	if !validClaimedModel(claimed) {
		return nil, requestError(ErrorInvalidModel)
	}
	requestModel := strings.TrimSpace(input.RequestModel)
	if requestModel == "" {
		requestModel = claimed
	}

	wanted := make([]string, 0, len(input.AccountIDs))
	seen := make(map[string]struct{}, len(input.AccountIDs))
	for _, id := range input.AccountIDs {
		trimmed := strings.TrimSpace(id)
		if trimmed == "" {
			continue
		}
		if _, dup := seen[trimmed]; dup {
			continue
		}
		seen[trimmed] = struct{}{}
		wanted = append(wanted, trimmed)
	}
	if len(wanted) == 0 {
		return nil, requestError(ErrorRequest)
	}
	if len(wanted) > maxTargetsPerSubmit {
		return nil, requestError(ErrorTooManyTargets)
	}

	adminAccountID, err := s.currentAdminAccountID(ctx, userID)
	if err != nil {
		return nil, err
	}
	session, err := s.sessions.RequireSession(ctx, userID, adminAccountID)
	if err != nil {
		return nil, err
	}
	accounts, err := s.reader.ListAdminAllAccounts(session)
	if err != nil {
		return nil, requestError(ErrorAccountsFetch)
	}
	byID := make(map[string]upstream.AdminGroupAccountInfo, len(accounts))
	for _, account := range accounts {
		byID[account.ID] = account
	}

	batchID := newID()
	jobs := make([]Job, 0, len(wanted))
	for _, accountID := range wanted {
		account, ok := byID[accountID]
		if !ok {
			return nil, requestError(ErrorTargetNotFound)
		}
		if !isOpenAIPlatform(account.Platform) || !isStaticKeyType(account.Type) {
			return nil, requestError(ErrorTargetIneligible)
		}
		jobs = append(jobs, Job{
			ID:              newID(),
			UserID:          userID,
			AdminAccountID:  adminAccountID,
			AccountID:       account.ID,
			AccountName:     account.Name,
			AccountPlatform: account.Platform,
			BaseURL:         account.BaseURL,
			Tier:            input.Tier,
			ClaimedModel:    claimed,
			RequestModel:    requestModel,
			Status:          StatusQueued,
			BatchID:         batchID,
		})
	}

	if err := s.repo.InsertJobs(ctx, jobs); err != nil {
		return nil, err
	}
	// 排完队顺手裁历史，保证「最多 100 份」这条约束不用等定时任务兜底。
	s.pruneHistory(ctx, userID, adminAccountID)
	s.worker.notify()
	return jobs, nil
}

// pruneHistory 把 workspace 的历史裁到 MaxRetainedJobs，并连带清掉检测器那边
// 对应会话的落盘目录。裁剪失败只记日志不阻断主流程——用户的检测已经排进去了，
// 不该因为清理旧数据失败就报错。
func (s *Service) pruneHistory(ctx context.Context, userID string, adminAccountID string) {
	sessions, err := s.repo.PruneJobs(ctx, userID, adminAccountID, MaxRetainedJobs)
	if err != nil {
		log.Printf("[purity_check] 裁剪历史失败 user=%s: %v", userID, err)
		return
	}
	if len(sessions) > 0 {
		log.Printf("[purity_check] 已裁剪 %d 条超出保留上限（%d）的历史", len(sessions), MaxRetainedJobs)
	}
	for _, sessionID := range sessions {
		s.removeDetectorRun(sessionID)
	}
}

// removeDetectorRun 删掉检测器旁路服务为某次会话留下的 SQLite 目录。
//
// 报告原文早已存进我们自己的库，那边的目录只剩排障价值，跟着任务一起删即可。
// sessionID 来自检测器，这里仍然校验一遍字符集：它会被拼进路径，
// 万一上游哪天改了 id 格式，不能让 ".." 之类的东西穿出目录。
func (s *Service) removeDetectorRun(sessionID string) {
	if s.detectorRunsDir == "" || sessionID == "" {
		return
	}
	for _, r := range sessionID {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
			log.Printf("[purity_check] 会话 id 含意外字符，跳过目录清理: %q", sessionID)
			return
		}
	}
	dir := filepath.Join(s.detectorRunsDir, "detector", "session-"+sessionID)
	if err := os.RemoveAll(dir); err != nil {
		log.Printf("[purity_check] 清理检测器目录失败 %s: %v", dir, err)
	}
}

// Delete 删除一条已结束的任务及其报告。
// 排队中/运行中的任务不能删，只能先取消——handler 会把这个区分传给前端。
func (s *Service) Delete(ctx context.Context, userID string, jobID string) error {
	adminAccountID, err := s.currentAdminAccountID(ctx, userID)
	if err != nil {
		return err
	}
	sessionID, deleted, err := s.repo.DeleteJob(ctx, jobID, userID, adminAccountID)
	if err != nil {
		return err
	}
	if !deleted {
		// 要么不存在/不属于本 workspace，要么还在排队或运行中。
		job, lookupErr := s.repo.GetJob(ctx, jobID, userID, adminAccountID)
		if lookupErr != nil {
			return lookupErr
		}
		if job == nil {
			return requestError(ErrorNotFound)
		}
		return requestError(ErrorNotDeletable)
	}
	s.removeDetectorRun(sessionID)
	return nil
}

// ---- 查询 ----

type JobListResponse struct {
	Jobs []Job `json:"jobs"`
	// QueuedTotal 是全局排队数（跨 workspace）。检测器只有一条队列，
	// 只报本 workspace 的数字会让人低估等待时间。
	QueuedTotal int `json:"queuedTotal"`
	// DetectorAvailable=false 时前端要提示检测器没部署/连不上。
	DetectorAvailable bool `json:"detectorAvailable"`
	// MaxRetained 是保留上限，前端用它提示「只保留最近 N 份，超出自动删除」。
	MaxRetained int `json:"maxRetained"`
}

func (s *Service) ListJobs(ctx context.Context, userID string, limit int) (JobListResponse, error) {
	var response JobListResponse
	adminAccountID, err := s.currentAdminAccountID(ctx, userID)
	if err != nil {
		return response, err
	}
	jobs, err := s.repo.ListJobs(ctx, userID, adminAccountID, limit)
	if err != nil {
		return response, err
	}

	// 失败和被取消的任务也可能有报告：探针全打不通时我们照样把报告存下来，
	// 里面的上游错误明细正是排障要看的。只取 succeeded 会把它们藏起来。
	ids := make([]string, 0, len(jobs))
	for _, job := range jobs {
		switch job.Status {
		case StatusSucceeded, StatusFailed, StatusCancelled:
			ids = append(ids, job.ID)
		}
	}
	summaries, err := s.repo.ListReportSummaries(ctx, ids)
	if err != nil {
		return response, err
	}
	for i := range jobs {
		if summary, ok := summaries[jobs[i].ID]; ok {
			// 列表页只带摘要，不带报告原文——原文动辄几十 KB，一页 100 条会很沉。
			summaryCopy := summary
			jobs[i].Report = &summaryCopy
		}
	}

	queued, err := s.repo.CountQueued(ctx)
	if err != nil {
		return response, err
	}

	response.Jobs = jobs
	response.QueuedTotal = queued
	response.DetectorAvailable = s.detector.Configured() && s.detector.Health(ctx) == nil
	response.MaxRetained = MaxRetainedJobs
	return response, nil
}

// JobDetail 返回任务加完整报告原文。
type JobDetail struct {
	Job Job `json:"job"`
	// ReportPayload 是检测器报告的 JSON 原文，直接透传给前端。
	// 【必须是 json.RawMessage 不能是 []byte】：encoding/json 会把 []byte
	// 编成 base64 字符串，前端拿到的就不是 JSON 对象了。
	ReportPayload json.RawMessage `json:"reportPayload,omitempty"`
}

func (s *Service) GetJob(ctx context.Context, userID string, jobID string) (JobDetail, error) {
	var detail JobDetail
	adminAccountID, err := s.currentAdminAccountID(ctx, userID)
	if err != nil {
		return detail, err
	}
	job, err := s.repo.GetJob(ctx, jobID, userID, adminAccountID)
	if err != nil {
		return detail, err
	}
	if job == nil {
		return detail, requestError(ErrorNotFound)
	}
	report, err := s.repo.GetReport(ctx, jobID)
	if err != nil {
		return detail, err
	}
	if report != nil {
		summary := *report
		payload := summary.Payload
		summary.Payload = nil
		job.Report = &summary
		detail.ReportPayload = payload
	}
	detail.Job = *job
	return detail, nil
}

// Cancel 取消任务。排队中的直接标记取消；正在跑的转发 stop 给检测器，
// 由 worker 收尾时落终态。
func (s *Service) Cancel(ctx context.Context, userID string, jobID string) error {
	adminAccountID, err := s.currentAdminAccountID(ctx, userID)
	if err != nil {
		return err
	}
	job, err := s.repo.GetJob(ctx, jobID, userID, adminAccountID)
	if err != nil {
		return err
	}
	if job == nil {
		return requestError(ErrorNotFound)
	}

	switch job.Status {
	case StatusQueued:
		cancelled, err := s.repo.CancelQueuedJob(ctx, jobID, userID, adminAccountID)
		if err != nil {
			return err
		}
		if cancelled {
			return nil
		}
		// 刚被 worker 领走，落到 running 分支重试一次。
		fallthrough
	case StatusRunning:
		// 检测器只有一个会话，stop 就是停当前那个。这里先确认当前跑的确实
		// 是这条任务，避免误停别人的。
		if err := s.worker.cancelRunning(ctx, jobID); err != nil {
			return err
		}
		return nil
	default:
		return requestError(ErrorNotCancellable)
	}
}

func (s *Service) requireSession(ctx context.Context, userID string) (upstream.Session, error) {
	adminAccountID, err := s.currentAdminAccountID(ctx, userID)
	if err != nil {
		return upstream.Session{}, err
	}
	return s.sessions.RequireSession(ctx, userID, adminAccountID)
}

// ---- worker 生命周期 ----

// Start 启动串行 worker，并把重启前残留的 running 任务放回队列。
func (s *Service) Start(ctx context.Context) {
	if !s.detector.Configured() {
		return
	}
	s.worker.start(ctx)
}

func (s *Service) Stop() { s.worker.stop() }

// resolveJobCredential 在真正启动某个任务前解析上游明文凭据。
// 返回值只在 worker 栈上停留，绝不落库、不写日志。
func (s *Service) resolveJobCredential(ctx context.Context, job Job) (upstream.ProbeCredential, error) {
	session, err := s.sessions.RequireSession(ctx, job.UserID, job.AdminAccountID)
	if err != nil {
		return upstream.ProbeCredential{}, err
	}
	accounts, err := s.reader.ListAdminAllAccounts(session)
	if err != nil {
		return upstream.ProbeCredential{}, requestError(ErrorAccountsFetch)
	}
	for _, account := range accounts {
		if account.ID != job.AccountID {
			continue
		}
		cred, err := s.reader.ResolveProbeCredential(session, account)
		if err != nil {
			return upstream.ProbeCredential{}, requestError(ErrorCredential)
		}
		return cred, nil
	}
	return upstream.ProbeCredential{}, requestError(ErrorTargetNotFound)
}
