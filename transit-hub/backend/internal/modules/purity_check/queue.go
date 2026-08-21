package purity_check

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

// worker 串行消费检测队列。
//
// 「串行」不是我们选的策略，是检测器的硬约束：它同一时刻只允许一个会话，
// 第二次 start 会直接 400。所以这里只有一个消费协程，跑完一个才领下一个。
//
// 多副本部署时两个进程各有一个 worker：领任务靠 SELECT ... FOR UPDATE
// SKIP LOCKED 不会重复领；真正启动时如果检测器已被另一个副本占用，会收到
// ErrDetectorBusy，那时把任务原位放回队列稍后重试，不算失败。
type worker struct {
	service *Service

	wake chan struct{}

	mu sync.Mutex
	// runningJobID 是本进程当前正在跑的任务。取消时要先核对它，
	// 免得把别人的会话停掉。
	runningJobID string

	cancelMu sync.Mutex
	stopOnce sync.Once
	cancel   context.CancelFunc
	done     chan struct{}
	// started 区分「worker 真的跑起来了」和「因为没配检测器压根没启动」。
	// 没启动时 loop 不会 close(done)，stop() 若无脑等就会白白拖慢关停 5 秒。
	started atomic.Bool
}

func newWorker(service *Service) *worker {
	return &worker{
		service: service,
		// 带缓冲=1：提交时的 notify 不阻塞，也不会漏掉唤醒。
		wake: make(chan struct{}, 1),
		done: make(chan struct{}),
	}
}

// pollInterval 是没被唤醒时的兜底轮询间隔。多副本下另一个进程放回队列的任务
// 不会 notify 到本进程，靠它兜底。
const pollInterval = 15 * time.Second

// statusPollInterval 是检测运行中拉进度的间隔。低档 19 条大约一分钟内跑完，
// 3 秒一次既能让前端进度条动起来，也不会把检测器打爆。
const statusPollInterval = 3 * time.Second

// maxJobDuration 是单个任务的墙钟上限。高档 158 条 + 32K 上下文可能跑很久，
// 但总得有个上限，否则检测器卡住会让整条队列永远堵着。
const maxJobDuration = 3 * time.Hour

func (w *worker) start(ctx context.Context) {
	runCtx, cancel := context.WithCancel(ctx)
	w.cancel = cancel

	if reset, err := w.service.repo.ResetStaleRunningJobs(runCtx); err != nil {
		log.Printf("[purity_check] 重置残留任务失败: %v", err)
	} else if reset > 0 {
		log.Printf("[purity_check] 已把 %d 个重启前残留的 running 任务放回队列", reset)
	}

	w.started.Store(true)
	go w.loop(runCtx)
}

func (w *worker) stop() {
	w.stopOnce.Do(func() {
		if w.cancel != nil {
			w.cancel()
		}
		if !w.started.Load() {
			// 从没启动过，没有 loop 会去关 done，不能在这儿干等。
			return
		}
		select {
		case <-w.done:
		case <-time.After(5 * time.Second):
		}
	})
}

func (w *worker) notify() {
	select {
	case w.wake <- struct{}{}:
	default:
	}
}

func (w *worker) loop(ctx context.Context) {
	defer close(w.done)
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		// 一直领到队列空为止，再回去等唤醒。
		for {
			if ctx.Err() != nil {
				return
			}
			picked, err := w.runNext(ctx)
			if err != nil {
				log.Printf("[purity_check] worker 出错: %v", err)
			}
			if !picked {
				break
			}
		}

		select {
		case <-ctx.Done():
			return
		case <-w.wake:
		case <-ticker.C:
		}
	}
}

// runNext 领一条任务并跑完。返回 false 表示队列空了。
func (w *worker) runNext(ctx context.Context) (bool, error) {
	job, err := w.service.repo.ClaimNextQueuedJob(ctx)
	if err != nil {
		return false, err
	}
	if job == nil {
		return false, nil
	}

	w.setRunning(job.ID)
	defer w.setRunning("")

	if err := w.execute(ctx, *job); err != nil {
		if errors.Is(err, ErrDetectorBusy) {
			// 检测器被别的副本占着。原位放回队列，等下一轮。
			// 这里主动睡一下，避免两个副本互相抢导致空转。
			if releaseErr := w.service.repo.ReleaseJob(context.WithoutCancel(ctx), job.ID); releaseErr != nil {
				log.Printf("[purity_check] 放回任务 %s 失败: %v", job.ID, releaseErr)
			}
			select {
			case <-ctx.Done():
			case <-time.After(statusPollInterval):
			}
			return false, nil
		}
		return true, err
	}
	return true, nil
}

// execute 跑完一个任务：取凭据 → start → 轮询 → 存报告。
func (w *worker) execute(ctx context.Context, job Job) error {
	jobCtx, cancel := context.WithTimeout(ctx, maxJobDuration)
	defer cancel()

	// 收尾用的 context：任务被取消或超时了，落终态这步不能跟着一起被取消，
	// 否则任务会永远卡在 running。
	finishCtx := context.WithoutCancel(ctx)

	cred, err := w.service.resolveJobCredential(jobCtx, job)
	if err != nil {
		return w.fail(finishCtx, job.ID, ErrorCredential, err.Error())
	}

	preset, err := w.service.detector.Preset(jobCtx, job.Tier)
	if err != nil {
		return w.fail(finishCtx, job.ID, ErrorDetectorUnavailable, err.Error())
	}

	started, err := w.service.detector.Start(jobCtx, StartRequest{
		BaseURL: cred.BaseURL,
		// 明文 key 只在这一行传递：进请求体、发给检测器，就地丢弃。
		APIKey:       cred.Key,
		ClaimedModel: job.ClaimedModel,
		RequestModel: job.RequestModel,
		Config:       preset,
	})
	if err != nil {
		if errors.Is(err, ErrDetectorBusy) {
			return err
		}
		return w.fail(finishCtx, job.ID, ErrorDetectorUnavailable, err.Error())
	}
	if err := w.service.repo.SetDetectorSession(finishCtx, job.ID, started.SessionID); err != nil {
		log.Printf("[purity_check] 记录会话 id 失败 job=%s: %v", job.ID, err)
	}

	status, err := w.waitForCompletion(jobCtx, job)
	if err != nil {
		// 超时或进程关停：让检测器也停下来，别留着一个孤儿会话把队列堵死。
		stopCtx, stopCancel := context.WithTimeout(finishCtx, 15*time.Second)
		_ = w.service.detector.Stop(stopCtx)
		stopCancel()
		return w.fail(finishCtx, job.ID, ErrorDetectorUnavailable, err.Error())
	}

	if !status.ReportAvailable {
		return w.fail(finishCtx, job.ID, ErrorDetectorUnavailable,
			"detector finished with status "+status.Status+" but produced no report")
	}

	payload, err := w.service.detector.Report(finishCtx)
	if err != nil {
		return w.fail(finishCtx, job.ID, ErrorDetectorUnavailable, err.Error())
	}

	summary := summarizeReport(payload)
	// 报告先存下来：哪怕这次没测成，里面的上游错误明细是排障的主要依据。
	if err := w.service.repo.SaveReport(finishCtx, job.ID, payload, summary); err != nil {
		return w.fail(finishCtx, job.ID, ErrorUnknown, err.Error())
	}

	// 一条探针都没成功 = 根本没测到任何东西，不是「检测结果」。
	//
	// 检测器这时仍然会给 operational_status=complete、verdict_available=true，
	// 并输出「Juice证据不足；指纹证据不明确」——那是它在如实描述"没有证据"，
	// 但对用户来说，上游 404 或连不上跟"这家中转可疑"是两回事，
	// 混在一起显示会把运维判断带偏。所以这里判失败，把上游错误直接摆出来。
	if summary.TotalRequests > 0 && summary.SuccessfulRequests == 0 {
		detail := summary.FailureHint
		if detail == "" {
			detail = "上游未返回任何成功响应"
		}
		return w.fail(finishCtx, job.ID, ErrorUpstreamUnreachable,
			detail+"（"+strconv.Itoa(summary.TotalRequests)+" 条探针全部失败）")
	}

	// 用户中途按了取消：检测器会以 stopped 收尾，报告仍然有效（是个残缺样本），
	// 但任务状态要如实记成 cancelled，不能报成成功。
	finalStatus := StatusSucceeded
	if status.Status == "stopped" {
		finalStatus = StatusCancelled
	}
	if err := w.service.repo.FinishJob(finishCtx, job.ID, finalStatus, "", ""); err != nil {
		return err
	}
	// 任务落终态才算真正进入历史，这时再裁一次。只在提交时裁不够：
	// 一批 30 个提交时它们还都是 queued，不占保留配额。
	w.service.pruneHistory(finishCtx, job.UserID, job.AdminAccountID)
	return nil
}

// waitForCompletion 轮询检测器直到终态，顺带把进度写回数据库。
func (w *worker) waitForCompletion(ctx context.Context, job Job) (StatusResponse, error) {
	ticker := time.NewTicker(statusPollInterval)
	defer ticker.Stop()

	var last StatusResponse
	for {
		select {
		case <-ctx.Done():
			return last, ctx.Err()
		case <-ticker.C:
		}

		status, err := w.service.detector.Status(ctx)
		if err != nil {
			// 单次轮询失败不判死刑：旁路服务可能只是短暂抖动，
			// 真出不来结果会被 maxJobDuration 兜住。
			log.Printf("[purity_check] 拉状态失败 job=%s: %v", job.ID, err)
			continue
		}
		last = status

		if status.Progress != nil {
			if err := w.service.repo.UpdateProgress(ctx, job.ID,
				status.Progress.Planned, status.Progress.LogicalCompleted, status.Progress.Errors); err != nil {
				log.Printf("[purity_check] 写进度失败 job=%s: %v", job.ID, err)
			}
		}

		if status.Terminal() {
			return status, nil
		}
	}
}

func (w *worker) fail(ctx context.Context, jobID string, errorKey string, detail string) error {
	if len(detail) > 1000 {
		detail = detail[:1000]
	}
	return w.service.repo.FinishJob(ctx, jobID, StatusFailed, errorKey, detail)
}

func (w *worker) setRunning(jobID string) {
	w.mu.Lock()
	w.runningJobID = jobID
	w.mu.Unlock()
}

// cancelRunning 停掉正在跑的检测。只有当本进程跑的确实是这条任务时才发 stop，
// 否则会把另一个副本正在跑的会话误停。
func (w *worker) cancelRunning(ctx context.Context, jobID string) error {
	w.mu.Lock()
	current := w.runningJobID
	w.mu.Unlock()
	if current != jobID {
		// 任务在别的副本上跑。单副本部署下走不到这里；多副本下如实报错，
		// 好过盲发 stop 停错会话。
		return requestError(ErrorNotCancellable)
	}

	w.cancelMu.Lock()
	defer w.cancelMu.Unlock()
	if err := w.service.detector.Stop(ctx); err != nil {
		return requestError(ErrorDetectorUnavailable)
	}
	// 落终态交给 execute 的收尾逻辑：检测器会把状态推到 stopped，
	// waitForCompletion 看到终态后照常拉报告并记成 cancelled。
	return nil
}

// summarizeReport 从报告原文里抽出列表页要用的几个结论字段。
//
// 只声明用得上的字段：json.Unmarshal 默认忽略未知字段，检测器以后加字段
// 不会让这里解析失败；报告原文照旧整份存进 payload，不丢信息。
// fingerprint_model 在「证据不明确」时是 null，指针接住后落库成空串。
func summarizeReport(payload []byte) Report {
	var parsed struct {
		OverallVerdict           string  `json:"overall_verdict"`
		OutcomeCode              string  `json:"outcome_code"`
		JuiceVerdictState        string  `json:"juice_verdict_state"`
		FingerprintModel         *string `json:"fingerprint_model"`
		FingerprintVerdictState  string  `json:"fingerprint_verdict_state"`
		FingerprintClaimMismatch bool    `json:"fingerprint_claim_mismatch"`
		Official                 bool    `json:"official"`
		QualityNote              string  `json:"quality_note"`
		NetworkSummary           struct {
			Successful   int `json:"successful"`
			LogicalTasks int `json:"logical_tasks"`
		} `json:"network_summary"`
		NetworkErrorDetails []struct {
			CategoryCN  string `json:"category_cn"`
			SafeMessage string `json:"safe_message"`
			HTTPStatus  *int   `json:"http_status"`
		} `json:"network_error_details"`
	}
	if err := json.Unmarshal(payload, &parsed); err != nil {
		// 解析不出摘要不影响原文已存下来，前端还能展开看完整报告。
		log.Printf("[purity_check] 解析报告摘要失败: %v", err)
		return Report{}
	}
	summary := Report{
		OverallVerdict:           parsed.OverallVerdict,
		OutcomeCode:              parsed.OutcomeCode,
		JuiceVerdictState:        parsed.JuiceVerdictState,
		FingerprintVerdictState:  parsed.FingerprintVerdictState,
		FingerprintClaimMismatch: parsed.FingerprintClaimMismatch,
		Official:                 parsed.Official,
		QualityNote:              parsed.QualityNote,
		SuccessfulRequests:       parsed.NetworkSummary.Successful,
		TotalRequests:            parsed.NetworkSummary.LogicalTasks,
	}
	if parsed.FingerprintModel != nil {
		summary.FingerprintModel = *parsed.FingerprintModel
	}
	// 取第一条上游错误做提示。检测器已对它做过脱敏（safe_message），可以直接展示。
	if len(parsed.NetworkErrorDetails) > 0 {
		first := parsed.NetworkErrorDetails[0]
		hint := first.SafeMessage
		if hint == "" {
			hint = first.CategoryCN
		}
		if first.HTTPStatus != nil && *first.HTTPStatus > 0 {
			hint += "（HTTP " + strconv.Itoa(*first.HTTPStatus) + "）"
		}
		summary.FailureHint = hint
	}
	return summary
}
