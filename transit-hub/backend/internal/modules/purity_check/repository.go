package purity_check

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Repository struct {
	db *pgxpool.Pool
}

func NewRepository(db *pgxpool.Pool) *Repository {
	return &Repository{db: db}
}

func newID() string {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		// crypto/rand 失败在实践中等于系统已经不可用，退回时间戳只是为了
		// 不让调用方拿到空 id。
		return hex.EncodeToString([]byte(time.Now().UTC().Format("20060102150405.000000000")))
	}
	return hex.EncodeToString(buf)
}

const jobColumns = `
	id, user_id, admin_account_id, account_id, account_name, account_platform, base_url,
	tier, claimed_model, request_model, status, batch_id, detector_session_id,
	planned_requests, completed_requests, failed_requests, error_key, error_detail,
	created_at, started_at, finished_at, updated_at
`

func scanJob(row pgx.Row) (Job, error) {
	var job Job
	err := row.Scan(
		&job.ID, &job.UserID, &job.AdminAccountID, &job.AccountID, &job.AccountName,
		&job.AccountPlatform, &job.BaseURL, &job.Tier, &job.ClaimedModel, &job.RequestModel,
		&job.Status, &job.BatchID, &job.DetectorSessionID,
		&job.PlannedRequests, &job.CompletedRequests, &job.FailedRequests,
		&job.ErrorKey, &job.ErrorDetail,
		&job.CreatedAt, &job.StartedAt, &job.FinishedAt, &job.UpdatedAt,
	)
	return job, err
}

// InsertJobs 把一批任务原子写入队列。批量提交要么全进要么全不进，
// 免得前端显示「提交了 5 个」实际只排上 3 个。
func (r *Repository) InsertJobs(ctx context.Context, jobs []Job) error {
	if len(jobs) == 0 {
		return nil
	}
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	for _, job := range jobs {
		_, err := tx.Exec(ctx, `
			INSERT INTO purity_check_jobs (
				id, user_id, admin_account_id, account_id, account_name, account_platform,
				base_url, tier, claimed_model, request_model, status, batch_id, planned_requests
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
		`, job.ID, job.UserID, job.AdminAccountID, job.AccountID, job.AccountName,
			job.AccountPlatform, job.BaseURL, string(job.Tier), job.ClaimedModel,
			job.RequestModel, string(StatusQueued), job.BatchID, job.PlannedRequests)
		if err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// ListJobs 返回某 workspace 的任务列表（倒序），并现算排队位次。
//
// 位次是全局的：检测器只有一个会话，所有 workspace 的排队任务共享同一条队列，
// 所以「你前面还有几个」必须跨 workspace 数，只数本 workspace 会骗人。
func (r *Repository) ListJobs(ctx context.Context, userID string, adminAccountID string, limit int) ([]Job, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := r.db.Query(ctx, `
		SELECT `+jobColumns+`,
			CASE WHEN status = 'queued' THEN (
				SELECT count(*) FROM purity_check_jobs q
				WHERE q.status = 'queued' AND q.created_at < j.created_at
			) ELSE 0 END AS queue_position
		FROM purity_check_jobs j
		WHERE user_id = $1 AND admin_account_id = $2
		ORDER BY created_at DESC
		LIMIT $3
	`, userID, adminAccountID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	jobs := make([]Job, 0, limit)
	for rows.Next() {
		var job Job
		err := rows.Scan(
			&job.ID, &job.UserID, &job.AdminAccountID, &job.AccountID, &job.AccountName,
			&job.AccountPlatform, &job.BaseURL, &job.Tier, &job.ClaimedModel, &job.RequestModel,
			&job.Status, &job.BatchID, &job.DetectorSessionID,
			&job.PlannedRequests, &job.CompletedRequests, &job.FailedRequests,
			&job.ErrorKey, &job.ErrorDetail,
			&job.CreatedAt, &job.StartedAt, &job.FinishedAt, &job.UpdatedAt,
			&job.QueuePosition,
		)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, job)
	}
	return jobs, rows.Err()
}

// GetJob 按 workspace 取单个任务，跨 workspace 取不到（防越权读别人的报告）。
func (r *Repository) GetJob(ctx context.Context, id string, userID string, adminAccountID string) (*Job, error) {
	job, err := scanJob(r.db.QueryRow(ctx, `
		SELECT `+jobColumns+` FROM purity_check_jobs
		WHERE id = $1 AND user_id = $2 AND admin_account_id = $3
	`, id, userID, adminAccountID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &job, nil
}

// ClaimNextQueuedJob 取队首任务并把它置为 running。
//
// FOR UPDATE SKIP LOCKED 保证多副本部署时两个 worker 不会领到同一条。
// 领到之后还要真正 start 成功才算数——检测器那边可能已被另一个副本占用，
// 那时调用方要用 ReleaseJob 把它放回队列。
func (r *Repository) ClaimNextQueuedJob(ctx context.Context) (*Job, error) {
	job, err := scanJob(r.db.QueryRow(ctx, `
		UPDATE purity_check_jobs SET
			status = 'running', started_at = now(), updated_at = now()
		WHERE id = (
			SELECT id FROM purity_check_jobs
			WHERE status = 'queued'
			ORDER BY created_at
			FOR UPDATE SKIP LOCKED
			LIMIT 1
		)
		RETURNING `+jobColumns+`
	`))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &job, nil
}

// ReleaseJob 把领过但没能启动的任务放回队列。
// 不动 created_at，所以它仍然排在原来的位置，不会被后来者插队。
func (r *Repository) ReleaseJob(ctx context.Context, id string) error {
	_, err := r.db.Exec(ctx, `
		UPDATE purity_check_jobs
		SET status = 'queued', started_at = NULL, detector_session_id = '', updated_at = now()
		WHERE id = $1 AND status = 'running'
	`, id)
	return err
}

func (r *Repository) SetDetectorSession(ctx context.Context, id string, sessionID string) error {
	_, err := r.db.Exec(ctx, `
		UPDATE purity_check_jobs SET detector_session_id = $2, updated_at = now()
		WHERE id = $1
	`, id, sessionID)
	return err
}

func (r *Repository) UpdateProgress(ctx context.Context, id string, planned int, completed int, failed int) error {
	_, err := r.db.Exec(ctx, `
		UPDATE purity_check_jobs
		SET planned_requests = $2, completed_requests = $3, failed_requests = $4, updated_at = now()
		WHERE id = $1 AND status = 'running'
	`, id, planned, completed, failed)
	return err
}

func (r *Repository) FinishJob(ctx context.Context, id string, status Status, errorKey string, errorDetail string) error {
	_, err := r.db.Exec(ctx, `
		UPDATE purity_check_jobs
		SET status = $2, error_key = $3, error_detail = $4, finished_at = now(), updated_at = now()
		WHERE id = $1
	`, id, string(status), errorKey, errorDetail)
	return err
}

// CancelQueuedJob 取消一个还在排队的任务。返回 false 表示它已经不是 queued
// 了（多半刚被 worker 领走），调用方应改走「停止运行中的检测」那条路。
func (r *Repository) CancelQueuedJob(ctx context.Context, id string, userID string, adminAccountID string) (bool, error) {
	tag, err := r.db.Exec(ctx, `
		UPDATE purity_check_jobs
		SET status = 'cancelled', finished_at = now(), updated_at = now()
		WHERE id = $1 AND user_id = $2 AND admin_account_id = $3 AND status = 'queued'
	`, id, userID, adminAccountID)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// ResetStaleRunningJobs 把重启前残留的 running 任务放回队列。
//
// 进程被 kill 时正在跑的任务会永远停在 running，队列就此卡死。检测器那边的
// 会话也随容器一起没了，重跑是唯一正确的处理。启动时调用一次。
func (r *Repository) ResetStaleRunningJobs(ctx context.Context) (int64, error) {
	tag, err := r.db.Exec(ctx, `
		UPDATE purity_check_jobs
		SET status = 'queued', started_at = NULL, detector_session_id = '',
			completed_requests = 0, failed_requests = 0, updated_at = now()
		WHERE status = 'running'
	`)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// SaveReport 存报告原文与摘要。payload 是检测器返回的 JSON 原文。
func (r *Repository) SaveReport(ctx context.Context, jobID string, payload []byte, summary Report) error {
	_, err := r.db.Exec(ctx, `
		INSERT INTO purity_check_reports (
			job_id, payload, overall_verdict, outcome_code, juice_verdict_state,
			fingerprint_model, fingerprint_verdict_state, fingerprint_claim_mismatch, official
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
		ON CONFLICT (job_id) DO UPDATE SET
			payload = EXCLUDED.payload,
			overall_verdict = EXCLUDED.overall_verdict,
			outcome_code = EXCLUDED.outcome_code,
			juice_verdict_state = EXCLUDED.juice_verdict_state,
			fingerprint_model = EXCLUDED.fingerprint_model,
			fingerprint_verdict_state = EXCLUDED.fingerprint_verdict_state,
			fingerprint_claim_mismatch = EXCLUDED.fingerprint_claim_mismatch,
			official = EXCLUDED.official
	`, jobID, payload, summary.OverallVerdict, summary.OutcomeCode,
		summary.JuiceVerdictState, summary.FingerprintModel, summary.FingerprintVerdictState,
		summary.FingerprintClaimMismatch, summary.Official)
	return err
}

// reportSummaryColumns 里的成功数/总数/质量说明直接从 payload 取，不另设列。
//
// 这样做有个实际好处：这些字段是后来才加的，走 JSONB 表达式意味着历史报告
// 立刻就能显示出来，不需要为了补一列去回填几百行。payload 本来就是事实来源。
const reportSummaryColumns = `
	overall_verdict, outcome_code, juice_verdict_state,
	fingerprint_model, fingerprint_verdict_state, fingerprint_claim_mismatch,
	official, created_at,
	coalesce(payload->>'quality_note', ''),
	coalesce((payload->'network_summary'->>'successful')::int, 0),
	coalesce((payload->'network_summary'->>'logical_tasks')::int, 0),
	coalesce(payload->'network_error_details'->0->>'safe_message', '')
`

func (r *Repository) GetReport(ctx context.Context, jobID string) (*Report, error) {
	var report Report
	report.JobID = jobID
	err := r.db.QueryRow(ctx, `
		SELECT payload, `+reportSummaryColumns+`
		FROM purity_check_reports WHERE job_id = $1
	`, jobID).Scan(
		&report.Payload, &report.OverallVerdict, &report.OutcomeCode, &report.JuiceVerdictState,
		&report.FingerprintModel, &report.FingerprintVerdictState, &report.FingerprintClaimMismatch,
		&report.Official, &report.CreatedAt,
		&report.QualityNote, &report.SuccessfulRequests, &report.TotalRequests, &report.FailureHint,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &report, nil
}

// ListReportSummaries 批量取一组任务的报告摘要，供列表页一次查完，避免 N+1。
func (r *Repository) ListReportSummaries(ctx context.Context, jobIDs []string) (map[string]Report, error) {
	out := make(map[string]Report, len(jobIDs))
	if len(jobIDs) == 0 {
		return out, nil
	}
	rows, err := r.db.Query(ctx, `
		SELECT job_id, `+reportSummaryColumns+`
		FROM purity_check_reports WHERE job_id = ANY($1)
	`, jobIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var report Report
		if err := rows.Scan(&report.JobID, &report.OverallVerdict, &report.OutcomeCode,
			&report.JuiceVerdictState, &report.FingerprintModel, &report.FingerprintVerdictState,
			&report.FingerprintClaimMismatch, &report.Official, &report.CreatedAt,
			&report.QualityNote, &report.SuccessfulRequests, &report.TotalRequests,
			&report.FailureHint); err != nil {
			return nil, err
		}
		out[report.JobID] = report
	}
	return out, rows.Err()
}

// CountQueued 返回全局排队任务数，用于「队列里还有 N 个」的展示。
func (r *Repository) CountQueued(ctx context.Context) (int, error) {
	var count int
	err := r.db.QueryRow(ctx, `SELECT count(*) FROM purity_check_jobs WHERE status = 'queued'`).Scan(&count)
	return count, err
}

// DeleteJob 删除一条已结束的任务（报告靠外键 ON DELETE CASCADE 一并删掉）。
//
// 只允许删终态：排队中和运行中的任务要走取消，直接删会让 worker 拿着一个
// 已经不存在的 job 继续跑，进度和终态都没地方写。
//
// 返回被删任务的检测器会话 id，调用方据此清理旁路服务那边的 SQLite 目录。
func (r *Repository) DeleteJob(ctx context.Context, id string, userID string, adminAccountID string) (string, bool, error) {
	var sessionID string
	err := r.db.QueryRow(ctx, `
		DELETE FROM purity_check_jobs
		WHERE id = $1 AND user_id = $2 AND admin_account_id = $3
		  AND status IN ('succeeded','failed','cancelled')
		RETURNING detector_session_id
	`, id, userID, adminAccountID).Scan(&sessionID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return sessionID, true, nil
}

// PruneJobs 把某个 workspace 的历史裁到最多 keep 条，删掉多出来的旧任务。
//
// 只数、只删终态任务：排队中和运行中的不占配额也不会被删，否则批量提交
// 30 个的时候会把自己刚排进去的任务削掉。
//
// 配额按 workspace 独立计算，不是全局共享——多个 workspace 时不该互相挤占。
func (r *Repository) PruneJobs(ctx context.Context, userID string, adminAccountID string, keep int) ([]string, error) {
	if keep < 0 {
		return nil, nil
	}
	rows, err := r.db.Query(ctx, `
		DELETE FROM purity_check_jobs
		WHERE id IN (
			SELECT id FROM purity_check_jobs
			WHERE user_id = $1 AND admin_account_id = $2
			  AND status IN ('succeeded','failed','cancelled')
			ORDER BY created_at DESC
			OFFSET $3
		)
		RETURNING detector_session_id
	`, userID, adminAccountID, keep)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	sessions := make([]string, 0)
	for rows.Next() {
		var sessionID string
		if err := rows.Scan(&sessionID); err != nil {
			return nil, err
		}
		if sessionID != "" {
			sessions = append(sessions, sessionID)
		}
	}
	return sessions, rows.Err()
}
