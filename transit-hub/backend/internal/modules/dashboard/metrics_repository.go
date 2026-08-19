package dashboard

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// MetricsRepository 负责 dashboard_daily_stats 表的持久化操作。
// 与 Redis 的 SessionStore 独立，专门用于存储每日统计快照。
type MetricsRepository struct {
	db *pgxpool.Pool
}

func NewMetricsRepository(db *pgxpool.Pool) *MetricsRepository {
	return &MetricsRepository{db: db}
}

// EnsureSchema 在服务启动时创建 dashboard_daily_stats 和 dashboard_balance_filter 表及索引，
// 并将旧数据迁移到 workspace 维度。
//
// dashboard_daily_stats: (user_id, admin_account_id, date) 唯一索引保证每工作区每天至多一行。
// dashboard_balance_filter: (user_id, admin_account_id) 唯一索引保证每工作区至多一行配置。
//
// 迁移策略：
//  1. 新增 admin_account_id 列（DEFAULT '' 兼容旧行）。
//  2. 删除旧的单维度唯一索引/约束，创建新的多维度唯一索引。
//  3. 旧数据 admin_account_id='' 保留原样，由 admin_accounts 统一归属迁移负责补值。
//  4. 币种分离：新增 today_profit_usd, site_balance_usd, today_purchase_cny, upstream_balance_cny, cost_status 列。
func (r *MetricsRepository) EnsureSchema(ctx context.Context) error {
	_, err := r.db.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS dashboard_daily_stats (
			id               text PRIMARY KEY,
			user_id          text NOT NULL,
			admin_account_id text NOT NULL DEFAULT '',
			date             date NOT NULL,
			today_profit     double precision NOT NULL DEFAULT 0,
			site_balance     double precision NOT NULL DEFAULT 0,
			today_purchase   double precision NOT NULL DEFAULT 0,
			net_profit       double precision NOT NULL DEFAULT 0,
			upstream_balance double precision NOT NULL DEFAULT 0,
			created_at       timestamptz NOT NULL DEFAULT now(),
			is_finalized     boolean NOT NULL DEFAULT false,
			finalized_at     timestamptz
		);

		-- 新增 admin_account_id 列（旧表迁移，IF NOT EXISTS 语义通过 DO NOTHING 实现）。
		DO $$ BEGIN
			ALTER TABLE dashboard_daily_stats ADD COLUMN admin_account_id text NOT NULL DEFAULT '';
		EXCEPTION WHEN duplicate_column THEN NULL;
		END $$;

		DO $$ BEGIN
			ALTER TABLE dashboard_daily_stats ADD COLUMN is_finalized boolean NOT NULL DEFAULT false;
		EXCEPTION WHEN duplicate_column THEN NULL;
		END $$;
		DO $$ BEGIN
			ALTER TABLE dashboard_daily_stats ADD COLUMN finalized_at timestamptz;
		EXCEPTION WHEN duplicate_column THEN NULL;
		END $$;

		-- 币种分离列：新增 USD/CNY 分开的字段 + cost_status。
		DO $$ BEGIN
			ALTER TABLE dashboard_daily_stats ADD COLUMN today_profit_usd double precision NOT NULL DEFAULT 0;
		EXCEPTION WHEN duplicate_column THEN NULL;
		END $$;
		DO $$ BEGIN
			ALTER TABLE dashboard_daily_stats ADD COLUMN site_balance_usd double precision NOT NULL DEFAULT 0;
		EXCEPTION WHEN duplicate_column THEN NULL;
		END $$;
		DO $$ BEGIN
			ALTER TABLE dashboard_daily_stats ADD COLUMN today_purchase_cny double precision NOT NULL DEFAULT 0;
		EXCEPTION WHEN duplicate_column THEN NULL;
		END $$;
		DO $$ BEGIN
			ALTER TABLE dashboard_daily_stats ADD COLUMN upstream_balance_cny double precision NOT NULL DEFAULT 0;
		EXCEPTION WHEN duplicate_column THEN NULL;
		END $$;
		DO $$ BEGIN
			ALTER TABLE dashboard_daily_stats ADD COLUMN cost_status text NOT NULL DEFAULT 'complete';
		EXCEPTION WHEN duplicate_column THEN NULL;
		END $$;

		-- 写入当天所用的汇率，历史行因此可复现。
		-- 不存这一列的话，Trends 只能用「当前」汇率去乘所有历史行，
		-- 改一次汇率整条历史曲线会被追溯改写。DEFAULT 7.0 与读取侧兜底一致。
		DO $$ BEGIN
			ALTER TABLE dashboard_daily_stats ADD COLUMN usd_to_cny_rate double precision NOT NULL DEFAULT 7.0;
		EXCEPTION WHEN duplicate_column THEN NULL;
		END $$;

		-- 旧列 -> 新列回填。缺这一步，已有安装的旧行新列全是 DEFAULT 0，
		-- 而 ListRange 只读新列，整条历史趋势会变成一条零线（真实数据还在旧列里，
		-- 属于可见性回退而非丢数）。
		-- 幂等：只在「新列仍为 0 且旧列非 0」时复制，重复执行不会二次覆盖已有值。
		-- net_profit 故意不回填：它就是 todayProfit(USD) - todayPurchase(CNY) 那个
		-- 混币种坏值，现在由 todayProfitCNY - todayPurchaseCNY 派生，不再落库。
		UPDATE dashboard_daily_stats SET
			today_profit_usd     = CASE WHEN today_profit_usd = 0     AND today_profit <> 0     THEN today_profit     ELSE today_profit_usd     END,
			site_balance_usd     = CASE WHEN site_balance_usd = 0     AND site_balance <> 0     THEN site_balance     ELSE site_balance_usd     END,
			today_purchase_cny   = CASE WHEN today_purchase_cny = 0   AND today_purchase <> 0   THEN today_purchase   ELSE today_purchase_cny   END,
			upstream_balance_cny = CASE WHEN upstream_balance_cny = 0 AND upstream_balance <> 0 THEN upstream_balance ELSE upstream_balance_cny END
		WHERE (today_profit_usd = 0     AND today_profit <> 0)
		   OR (site_balance_usd = 0     AND site_balance <> 0)
		   OR (today_purchase_cny = 0   AND today_purchase <> 0)
		   OR (upstream_balance_cny = 0 AND upstream_balance <> 0);

		-- 删除旧的 (user_id, date) 唯一索引，避免与新索引冲突。
		DROP INDEX IF EXISTS idx_dashboard_daily_stats_user_date;

		-- 创建新的 (user_id, admin_account_id, date) 唯一索引。
		CREATE UNIQUE INDEX IF NOT EXISTS idx_dashboard_daily_stats_user_account_date
			ON dashboard_daily_stats (user_id, admin_account_id, date);
		CREATE INDEX IF NOT EXISTS idx_dashboard_daily_stats_user_date_desc
			ON dashboard_daily_stats (user_id, admin_account_id, date DESC);

		CREATE TABLE IF NOT EXISTS dashboard_balance_filter (
			user_id          text NOT NULL,
			admin_account_id text NOT NULL DEFAULT '',
			exclude_admin    boolean NOT NULL DEFAULT true,
			exclude_balances jsonb NOT NULL DEFAULT '[]',
			usd_to_cny_rate  double precision NOT NULL DEFAULT 7.0,
			updated_at       timestamptz NOT NULL DEFAULT now()
		);

		-- 新增 admin_account_id 列（旧表迁移）。
		DO $$ BEGIN
			ALTER TABLE dashboard_balance_filter ADD COLUMN admin_account_id text NOT NULL DEFAULT '';
		EXCEPTION WHEN duplicate_column THEN NULL;
		END $$;

		-- 新增 usd_to_cny_rate 列（旧表迁移）。营收/站点余额从 USD 折算到 CNY 的倍率。
		-- DEFAULT 7.0 而非 0：0 会把营收乘成 0，读取侧 EffectiveUSDToCNYRate 也做了同样的兜底。
		DO $$ BEGIN
			ALTER TABLE dashboard_balance_filter ADD COLUMN usd_to_cny_rate double precision NOT NULL DEFAULT 7.0;
		EXCEPTION WHEN duplicate_column THEN NULL;
		END $$;

		-- 汇率必须为正：0 或负值会把营收乘成 0/负数，是我们要防的那类静默错账。
		-- NOT VALID 只校验新写入，不扫历史行，避免既有脏数据导致服务启动失败。
		DO $$ BEGIN
			ALTER TABLE dashboard_balance_filter
				ADD CONSTRAINT dashboard_balance_filter_rate_positive
				CHECK (usd_to_cny_rate > 0) NOT VALID;
		EXCEPTION WHEN duplicate_object THEN NULL;
		END $$;

		-- 删除旧的 user_id 主键约束，改为复合唯一索引。
		-- 旧表可能用 user_id 做主键或唯一约束，需要先去除。
		ALTER TABLE dashboard_balance_filter DROP CONSTRAINT IF EXISTS dashboard_balance_filter_pkey;
		CREATE UNIQUE INDEX IF NOT EXISTS idx_dashboard_balance_filter_user_account
			ON dashboard_balance_filter (user_id, admin_account_id);
	`)
	return err
}

// Upsert 插入或更新指定用户指定工作区指定日期的快照行。
// 冲突时用最新的指标值覆盖旧值，保证一天内多次调用始终保留最新数据。
func (r *MetricsRepository) Upsert(ctx context.Context, snapshot DailySnapshot) error {
	_, err := r.db.Exec(ctx, `
		INSERT INTO dashboard_daily_stats (id, user_id, admin_account_id, date, today_profit_usd, site_balance_usd, today_purchase_cny, upstream_balance_cny, cost_status, usd_to_cny_rate, created_at, is_finalized, finalized_at)
		SELECT $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13
		WHERE EXISTS (SELECT 1 FROM admin_accounts WHERE user_id = $2 AND id = $3)
		ON CONFLICT (user_id, admin_account_id, date) DO UPDATE SET
			today_profit_usd     = EXCLUDED.today_profit_usd,
			site_balance_usd     = EXCLUDED.site_balance_usd,
			today_purchase_cny   = EXCLUDED.today_purchase_cny,
			upstream_balance_cny = EXCLUDED.upstream_balance_cny,
			cost_status          = EXCLUDED.cost_status,
			usd_to_cny_rate      = EXCLUDED.usd_to_cny_rate,
			created_at           = EXCLUDED.created_at,
			is_finalized         = EXCLUDED.is_finalized,
			finalized_at         = EXCLUDED.finalized_at
		WHERE NOT dashboard_daily_stats.is_finalized
		  AND EXISTS (SELECT 1 FROM admin_accounts WHERE user_id = EXCLUDED.user_id AND id = EXCLUDED.admin_account_id)
	`, snapshot.ID, snapshot.UserID, snapshot.AdminAccountID, snapshot.Date, snapshot.TodayProfitUSD, snapshot.SiteBalanceUSD,
		snapshot.TodayPurchaseCNY, snapshot.UpstreamBalanceCNY, snapshot.CostStatus, snapshot.EffectiveRate(), snapshot.CreatedAt, snapshot.IsFinalized, snapshot.FinalizedAt)
	return err
}

// ListRange 查询指定用户指定工作区最近 days 天的快照记录，按日期升序返回。
// 不包含当天（当天的数据由 LiveMetrics 实时提供），仅返回已保存的历史日期。
func (r *MetricsRepository) ListRange(ctx context.Context, userID, adminAccountID string, days int, today string) ([]DailySnapshot, error) {
	rows, err := r.db.Query(ctx, `
		SELECT id, user_id, admin_account_id, date, today_profit_usd, site_balance_usd, today_purchase_cny, upstream_balance_cny, COALESCE(cost_status, 'complete'), COALESCE(usd_to_cny_rate, 0), created_at, finalized_at, is_finalized
		FROM dashboard_daily_stats
		-- A draft remains visible while the reconciler retries it. This avoids
		-- a historical gap after restarts or temporary upstream failures; only
		-- finalized rows are protected from later writes.
		WHERE user_id = $1 AND admin_account_id = $2 AND date >= ($4::date - $3::int) AND date < $4::date
		ORDER BY date ASC
	`, userID, adminAccountID, days, today)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	snapshots := make([]DailySnapshot, 0)
	for rows.Next() {
		var s DailySnapshot
		// Scan 目标数必须与上面 SELECT 的列数严格一致（13 个），顺序也必须一一对应。
		// pgx 在数量不符时直接返回错误，而 go build / 不连库的 go test 都发现不了，
		// 症状是 /api/dashboard/trends 稳定 500、整个仪表盘报「加载指标失败」。
		if err := rows.Scan(&s.ID, &s.UserID, &s.AdminAccountID, &s.Date, &s.TodayProfitUSD, &s.SiteBalanceUSD,
			&s.TodayPurchaseCNY, &s.UpstreamBalanceCNY, &s.CostStatus, &s.USDToCNYRate,
			&s.CreatedAt, &s.FinalizedAt, &s.IsFinalized); err != nil {
			return nil, err
		}
		snapshots = append(snapshots, s)
	}
	return snapshots, rows.Err()
}

func (r *MetricsRepository) IsFinalized(ctx context.Context, userID, adminAccountID, date string) (bool, error) {
	var finalized bool
	err := r.db.QueryRow(ctx, `SELECT is_finalized FROM dashboard_daily_stats WHERE user_id = $1 AND admin_account_id = $2 AND date = $3::date`, userID, adminAccountID, date).Scan(&finalized)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return finalized, err
}

// Exists 检查指定用户指定工作区指定日期是否已有快照行。
func (r *MetricsRepository) Exists(ctx context.Context, userID, adminAccountID string, date time.Time) (bool, error) {
	var count int
	err := r.db.QueryRow(ctx, `
		SELECT COUNT(*) FROM dashboard_daily_stats WHERE user_id = $1 AND admin_account_id = $2 AND date = $3
	`, userID, adminAccountID, date).Scan(&count)
	return count > 0, err
}

// GetBalanceFilter 读取指定用户指定工作区的余额筛选配置。
// 若用户尚未配置，返回默认值（排除 admin、不排除任何余额值）。
func (r *MetricsRepository) GetBalanceFilter(ctx context.Context, userID, adminAccountID string) (BalanceFilterConfig, error) {
	config := BalanceFilterConfig{
		UserID:          userID,
		AdminAccountID:  adminAccountID,
		ExcludeAdmin:    true,
		ExcludeBalances: []float64{},
	}
	var balancesJSON []byte
	err := r.db.QueryRow(ctx, `
		SELECT exclude_admin, exclude_balances, COALESCE(usd_to_cny_rate, 7.0)
		FROM dashboard_balance_filter WHERE user_id = $1 AND admin_account_id = $2
	`, userID, adminAccountID).Scan(&config.ExcludeAdmin, &balancesJSON, &config.USDToCNYRate)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return config, nil
		}
		return config, err
	}
	if len(balancesJSON) > 0 {
		if err := json.Unmarshal(balancesJSON, &config.ExcludeBalances); err != nil {
			return config, err
		}
	}
	return config, nil
}

// SaveBalanceFilter 保存或更新指定用户指定工作区的余额筛选配置。
// 使用 upsert 确保幂等写入，用户首次配置和后续修改都走同一路径。
func (r *MetricsRepository) SaveBalanceFilter(ctx context.Context, config BalanceFilterConfig) error {
	balancesJSON, err := json.Marshal(config.ExcludeBalances)
	if err != nil {
		return err
	}
	// 汇率传原始值，兜底逻辑放在 SQL 里做「传了才覆盖，没传保留原值」：
	//   - 调用方漏传（反序列化成 0）时，UPDATE 分支保留库里已配置的汇率，
	//     不能用 EXCLUDED.usd_to_cny_rate 无条件覆盖，否则改一次余额过滤条件
	//     就会把已配置的汇率静默打回默认值。
	//   - INSERT 分支用 NULLIF+COALESCE 把 0 变成默认值，满足
	//     usd_to_cny_rate > 0 的 CHECK 约束。
	_, err = r.db.Exec(ctx, `
		INSERT INTO dashboard_balance_filter (user_id, admin_account_id, exclude_admin, exclude_balances, usd_to_cny_rate, updated_at)
		VALUES ($1, $2, $3, $4, COALESCE(NULLIF($5::double precision, 0), $6::double precision), now())
		ON CONFLICT (user_id, admin_account_id) DO UPDATE SET
			exclude_admin    = EXCLUDED.exclude_admin,
			exclude_balances = EXCLUDED.exclude_balances,
			usd_to_cny_rate  = COALESCE(
				NULLIF($5::double precision, 0),
				dashboard_balance_filter.usd_to_cny_rate,
				$6::double precision
			),
			updated_at       = now()
	`, config.UserID, config.AdminAccountID, config.ExcludeAdmin, balancesJSON, config.USDToCNYRate, DefaultUSDToCNYRate)
	return err
}
