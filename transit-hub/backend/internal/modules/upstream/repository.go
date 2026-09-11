package upstream

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
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

func (r *Repository) InsertMultiplierEvent(ctx context.Context, event MultiplierEvent) error {
	if event.ID == "" {
		id, err := randomMultiplierEventID()
		if err != nil {
			return err
		}
		event.ID = id
	}
	if event.ObservedAt.IsZero() {
		event.ObservedAt = time.Now().UTC()
	}
	_, err := r.db.Exec(ctx, `
		INSERT INTO upstream_group_multiplier_events
			(id, user_id, admin_account_id, site_id, site_name, group_id, group_name,
			 previous_multiplier, current_multiplier, mapped, notified, observed_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
	`, event.ID, event.UserID, event.AdminAccountID, event.SiteID, event.SiteName, event.GroupID,
		event.GroupName, event.PreviousMultiplier, event.CurrentMultiplier, event.Mapped,
		event.Notified, event.ObservedAt.UTC())
	return err
}

func (r *Repository) ListMultiplierEventsSince(ctx context.Context, userID, adminAccountID string, since time.Time, mappedOnly *bool) ([]MultiplierEvent, error) {
	query := `
		SELECT id, user_id, admin_account_id, site_id, site_name, group_id, group_name,
		       previous_multiplier, current_multiplier, mapped, notified, observed_at
		FROM upstream_group_multiplier_events
		WHERE user_id = $1 AND admin_account_id = $2 AND observed_at >= $3`
	args := []any{userID, adminAccountID, since.UTC()}
	if mappedOnly != nil {
		query += " AND mapped = $4"
		args = append(args, *mappedOnly)
	}
	query += " ORDER BY observed_at ASC, id ASC"

	rows, err := r.db.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make([]MultiplierEvent, 0)
	for rows.Next() {
		var event MultiplierEvent
		if err := rows.Scan(&event.ID, &event.UserID, &event.AdminAccountID, &event.SiteID, &event.SiteName,
			&event.GroupID, &event.GroupName, &event.PreviousMultiplier, &event.CurrentMultiplier,
			&event.Mapped, &event.Notified, &event.ObservedAt); err != nil {
			return nil, err
		}
		result = append(result, event)
	}
	return result, rows.Err()
}

func (r *Repository) PruneMultiplierEventsBefore(ctx context.Context, cutoff time.Time) (int64, error) {
	result, err := r.db.Exec(ctx, `DELETE FROM upstream_group_multiplier_events WHERE observed_at < $1`, cutoff.UTC())
	if err != nil {
		return 0, err
	}
	return result.RowsAffected(), nil
}

func randomMultiplierEventID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate multiplier event id: %w", err)
	}
	return "mue_" + hex.EncodeToString(b), nil
}

func (r *Repository) EnsureSchema(ctx context.Context) error {
	// upstream_sites is intentionally independent from group_rate_snapshots: site
	// configuration and session restore are operational state, while historical
	// multiplier snapshots must remain readable even after a site is deleted.
	if _, err := r.db.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS upstream_sites (
			id text PRIMARY KEY,
			user_id text NOT NULL DEFAULT '',
			name text NOT NULL,
			base_url text NOT NULL,
			platform text NOT NULL,
			requested_platform text NOT NULL,
			account text NOT NULL,
			remark text NOT NULL DEFAULT '',
			recharge_rate double precision NOT NULL DEFAULT 1,
			status text NOT NULL,
			error_key text NULL,
			metrics jsonb NOT NULL,
			session jsonb NULL,
			last_synced_at bigint NULL,
			created_at timestamptz NOT NULL,
			updated_at timestamptz NOT NULL
		)
	`); err != nil {
		return err
	}
	if _, err := r.db.Exec(ctx, `
		ALTER TABLE upstream_sites ADD COLUMN IF NOT EXISTS user_id text NOT NULL DEFAULT ''
	`); err != nil {
		return err
	}
	if _, err := r.db.Exec(ctx, `
		ALTER TABLE upstream_sites ADD COLUMN IF NOT EXISTS settings jsonb NOT NULL DEFAULT '{}'::jsonb
	`); err != nil {
		return err
	}
	// 工作区隔离字段：每个站点归属到一个 admin workspace。
	if _, err := r.db.Exec(ctx, `
		ALTER TABLE upstream_sites ADD COLUMN IF NOT EXISTS admin_account_id text NOT NULL DEFAULT ''
	`); err != nil {
		return err
	}
	if _, err := r.db.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS upstream_site_recharges (
			id text PRIMARY KEY,
			user_id text NOT NULL,
			admin_account_id text NOT NULL,
			site_id text NOT NULL REFERENCES upstream_sites(id) ON DELETE CASCADE,
			amount double precision NOT NULL CHECK (amount > 0),
			note text NOT NULL DEFAULT '',
			created_at timestamptz NOT NULL DEFAULT now()
		)
	`); err != nil {
		return err
	}
	if _, err := r.db.Exec(ctx, `
		CREATE INDEX IF NOT EXISTS idx_upstream_site_recharges_site_created
		ON upstream_site_recharges (site_id, created_at DESC, id DESC)
	`); err != nil {
		return err
	}
	if _, err := r.db.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS upstream_site_daily_usage (
			site_id text NOT NULL REFERENCES upstream_sites(id) ON DELETE CASCADE,
			usage_date date NOT NULL,
			group_name text NOT NULL,
			raw_amount double precision NOT NULL CHECK (raw_amount >= 0),
			multiplier double precision NOT NULL CHECK (multiplier >= 0),
			adjusted_amount double precision NOT NULL CHECK (adjusted_amount >= 0),
			updated_at timestamptz NOT NULL DEFAULT now(),
			PRIMARY KEY (site_id, usage_date, group_name)
		)
	`); err != nil {
		return err
	}
	if _, err := r.db.Exec(ctx, `
		WITH single_user AS (
			SELECT min(id) AS id
			FROM users
			HAVING count(*) = 1
		)
		UPDATE upstream_sites
		SET user_id = single_user.id
		FROM single_user
		WHERE upstream_sites.user_id = ''
	`); err != nil {
		return err
	}
	_, err := r.db.Exec(ctx, `
		CREATE INDEX IF NOT EXISTS idx_upstream_sites_user_created
		ON upstream_sites (user_id, created_at ASC, id ASC)
	`)
	return err
}

func (r *Repository) AddRecharge(ctx context.Context, userID, adminAccountID, siteID string, entry RechargeEntry) error {
	createdAt := time.UnixMilli(entry.CreatedAt)
	if entry.CreatedAt == 0 {
		createdAt = time.Now()
	}
	result, err := r.db.Exec(ctx, `
		INSERT INTO upstream_site_recharges (id, user_id, admin_account_id, site_id, amount, note, created_at)
		SELECT $1, $2, $3, $4, $5, $6, $7
		WHERE EXISTS (
			SELECT 1 FROM upstream_sites
			WHERE id = $4 AND user_id = $2 AND admin_account_id = $3
		)
	`, entry.ID, userID, adminAccountID, siteID, entry.Amount, entry.Note, createdAt)
	if err != nil {
		return err
	}
	if result.RowsAffected() == 0 {
		return newRequestError(ErrorNotFound, "")
	}
	return nil
}

func (r *Repository) ListRecharges(ctx context.Context, userID, adminAccountID, siteID string) ([]RechargeEntry, error) {
	rows, err := r.db.Query(ctx, `
		SELECT id, amount, note, (extract(epoch FROM created_at) * 1000)::bigint
		FROM upstream_site_recharges
		WHERE user_id = $1 AND admin_account_id = $2 AND site_id = $3
		ORDER BY created_at DESC, id DESC
	`, userID, adminAccountID, siteID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	entries := make([]RechargeEntry, 0)
	for rows.Next() {
		var entry RechargeEntry
		if err := rows.Scan(&entry.ID, &entry.Amount, &entry.Note, &entry.CreatedAt); err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	return entries, rows.Err()
}

func (r *Repository) UpsertDailyUsage(ctx context.Context, siteID, usageDate, groupName string, rawAmount, multiplier, adjustedAmount float64) error {
	_, err := r.db.Exec(ctx, `
		INSERT INTO upstream_site_daily_usage (site_id, usage_date, group_name, raw_amount, multiplier, adjusted_amount)
		VALUES ($1, $2::date, $3, $4, $5, $6)
		ON CONFLICT (site_id, usage_date, group_name) DO UPDATE SET
			raw_amount = EXCLUDED.raw_amount,
			multiplier = EXCLUDED.multiplier,
			adjusted_amount = EXCLUDED.adjusted_amount,
			updated_at = now()
	`, siteID, usageDate, groupName, rawAmount, multiplier, adjustedAmount)
	return err
}

func (r *Repository) ManualAccountingSummary(ctx context.Context, userID, adminAccountID, siteID string) (ManualAccountingSummary, error) {
	var summary ManualAccountingSummary
	err := r.db.QueryRow(ctx, `
		SELECT
			COALESCE((SELECT sum(amount) FROM upstream_site_recharges WHERE user_id = $1 AND admin_account_id = $2 AND site_id = $3), 0),
			COALESCE((SELECT sum(u.adjusted_amount) FROM upstream_site_daily_usage u
				INNER JOIN upstream_sites s ON u.site_id = s.id
				WHERE u.site_id = $3 AND s.user_id = $1 AND s.admin_account_id = $2), 0)
	`, userID, adminAccountID, siteID).Scan(&summary.RechargeTotal, &summary.ConsumedTotal)
	return summary, err
}

func (r *Repository) UpsertBalanceSnapshot(ctx context.Context, snapshot BalanceSnapshot) error {
	_, err := r.db.Exec(ctx, `
		INSERT INTO upstream_site_balance_snapshots (site_id, snapshot_date, balance_usd, balance_cny, recharge_rate)
		VALUES ($1, $2::date, $3, $4, $5)
		ON CONFLICT (site_id, snapshot_date) DO UPDATE SET
			balance_usd = EXCLUDED.balance_usd,
			balance_cny = EXCLUDED.balance_cny,
			recharge_rate = EXCLUDED.recharge_rate,
			created_at = now()
	`, snapshot.SiteID, snapshot.SnapshotDate, snapshot.BalanceUSD, snapshot.BalanceCNY, snapshot.RechargeRate)
	return err
}

func (r *Repository) GetBalanceSnapshot(ctx context.Context, siteID, snapshotDate string) (*BalanceSnapshot, error) {
	var snapshot BalanceSnapshot
	var createdAt time.Time
	err := r.db.QueryRow(ctx, `
		SELECT site_id, snapshot_date::text, balance_usd, balance_cny, recharge_rate, created_at
		FROM upstream_site_balance_snapshots
		WHERE site_id = $1 AND snapshot_date = $2::date
	`, siteID, snapshotDate).Scan(&snapshot.SiteID, &snapshot.SnapshotDate, &snapshot.BalanceUSD, &snapshot.BalanceCNY, &snapshot.RechargeRate, &createdAt)
	if err != nil {
		return nil, err
	}
	snapshot.CreatedAt = createdAt.Unix()
	return &snapshot, nil
}

func (r *Repository) ListBalanceSnapshots(ctx context.Context, siteID string, startDate, endDate string) ([]BalanceSnapshot, error) {
	rows, err := r.db.Query(ctx, `
		SELECT site_id, snapshot_date::text, balance_usd, balance_cny, recharge_rate, created_at
		FROM upstream_site_balance_snapshots
		WHERE site_id = $1 AND snapshot_date >= $2::date AND snapshot_date <= $3::date
		ORDER BY snapshot_date ASC
	`, siteID, startDate, endDate)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var snapshots []BalanceSnapshot
	for rows.Next() {
		var snapshot BalanceSnapshot
		var createdAt time.Time
		if err := rows.Scan(&snapshot.SiteID, &snapshot.SnapshotDate, &snapshot.BalanceUSD, &snapshot.BalanceCNY, &snapshot.RechargeRate, &createdAt); err != nil {
			return nil, err
		}
		snapshot.CreatedAt = createdAt.Unix()
		snapshots = append(snapshots, snapshot)
	}
	return snapshots, rows.Err()
}

func (r *Repository) ListSites(ctx context.Context) ([]Site, error) {
	rows, err := r.db.Query(ctx, `
		SELECT id, user_id, admin_account_id, name, base_url, platform, requested_platform, account, remark,
			recharge_rate, status, error_key, metrics, session, settings, last_synced_at
		FROM upstream_sites
		WHERE user_id <> ''
		ORDER BY created_at ASC, id ASC
	`)
	if err != nil {
		return nil, err
	}
	return scanSites(rows)
}

func (r *Repository) ListSitesForUser(ctx context.Context, userID string) ([]Site, error) {
	rows, err := r.db.Query(ctx, `
		SELECT id, user_id, admin_account_id, name, base_url, platform, requested_platform, account, remark,
			recharge_rate, status, error_key, metrics, session, settings, last_synced_at
		FROM upstream_sites
		WHERE user_id = $1
		ORDER BY created_at ASC, id ASC
	`, userID)
	if err != nil {
		return nil, err
	}
	return scanSites(rows)
}

func (r *Repository) SaveSite(ctx context.Context, site Site) error {
	metricsJSON, err := json.Marshal(site.Metrics)
	if err != nil {
		return err
	}

	var sessionJSON []byte
	if site.Session != nil {
		sessionJSON, err = json.Marshal(site.Session)
		if err != nil {
			return err
		}
	}

	settingsJSON, err := json.Marshal(site.Settings)
	if err != nil {
		return err
	}

	now := time.Now()
	result, err := r.db.Exec(ctx, `
		INSERT INTO upstream_sites (
			id, user_id, admin_account_id, name, base_url, platform, requested_platform, account, remark,
			recharge_rate, status, error_key, metrics, session, settings, last_synced_at,
			created_at, updated_at
		)
		SELECT $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13::jsonb, $14::jsonb, $15::jsonb, $16, $17, $17
		WHERE EXISTS (SELECT 1 FROM admin_accounts WHERE user_id = $2 AND id = $3)
		ON CONFLICT (id) DO UPDATE SET
			user_id = EXCLUDED.user_id,
			admin_account_id = EXCLUDED.admin_account_id,
			name = EXCLUDED.name,
			base_url = EXCLUDED.base_url,
			platform = EXCLUDED.platform,
			requested_platform = EXCLUDED.requested_platform,
			account = EXCLUDED.account,
			remark = EXCLUDED.remark,
			recharge_rate = EXCLUDED.recharge_rate,
			status = EXCLUDED.status,
			error_key = EXCLUDED.error_key,
			metrics = EXCLUDED.metrics,
			session = EXCLUDED.session,
			settings = EXCLUDED.settings,
			last_synced_at = EXCLUDED.last_synced_at,
			updated_at = EXCLUDED.updated_at
		WHERE EXISTS (SELECT 1 FROM admin_accounts WHERE user_id = EXCLUDED.user_id AND id = EXCLUDED.admin_account_id)
	`, site.ID, site.UserID, site.AdminAccountID, site.Name, site.BaseURL, site.Platform, site.RequestedPlatform, site.Account, site.Remark,
		site.RechargeRate, site.Status, site.ErrorKey, string(metricsJSON), nullableJSONString(sessionJSON), string(settingsJSON), site.LastSyncedAt, now)
	if err != nil {
		return err
	}
	if result.RowsAffected() == 0 {
		return newRequestError(ErrorNotFound, "")
	}
	return nil
}

func (r *Repository) DeleteSite(ctx context.Context, userID string, id string) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	// group_rate_snapshots intentionally stays independent from upstream_sites for
	// reads, but deleting a station is a user-visible lifecycle action and must
	// clear the station's multiplier history in the same transaction.
	if _, err := tx.Exec(ctx, `DELETE FROM group_rate_snapshots WHERE user_id = $1 AND site_id = $2`, userID, id); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM upstream_sites WHERE user_id = $1 AND id = $2`, userID, id); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func scanSites(rows pgx.Rows) ([]Site, error) {
	defer rows.Close()

	sites := make([]Site, 0)
	for rows.Next() {
		var site Site
		var metricsJSON []byte
		var sessionJSON []byte
		var settingsJSON []byte
		if err := rows.Scan(
			&site.ID,
			&site.UserID,
			&site.AdminAccountID,
			&site.Name,
			&site.BaseURL,
			&site.Platform,
			&site.RequestedPlatform,
			&site.Account,
			&site.Remark,
			&site.RechargeRate,
			&site.Status,
			&site.ErrorKey,
			&metricsJSON,
			&sessionJSON,
			&settingsJSON,
			&site.LastSyncedAt,
		); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(metricsJSON, &site.Metrics); err != nil {
			return nil, err
		}
		if len(sessionJSON) > 0 {
			var session Session
			if err := json.Unmarshal(sessionJSON, &session); err != nil {
				return nil, err
			}
			site.Session = &session
		}
		if len(settingsJSON) > 0 {
			_ = json.Unmarshal(settingsJSON, &site.Settings)
		}
		sites = append(sites, site)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return sites, nil
}

func nullableJSONString(value []byte) any {
	if len(value) == 0 {
		return nil
	}
	return string(value)
}
