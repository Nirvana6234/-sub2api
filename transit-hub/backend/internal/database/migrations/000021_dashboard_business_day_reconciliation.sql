-- Dashboard history uses an explicit Shanghai business day. Older records
-- remain visible while the three most recent completed days are reopened for
-- same-day revenue and upstream-cost reconciliation by the application.
DO $$
BEGIN
    -- Fresh installs create this legacy table through MetricsRepository after
    -- migrations. Existing installations need the compatibility migration.
    IF to_regclass('public.dashboard_daily_stats') IS NULL THEN
        RETURN;
    END IF;

    ALTER TABLE dashboard_daily_stats
        ADD COLUMN IF NOT EXISTS is_finalized boolean NOT NULL DEFAULT false;

    ALTER TABLE dashboard_daily_stats
        ADD COLUMN IF NOT EXISTS finalized_at timestamptz;

    UPDATE dashboard_daily_stats
    SET is_finalized = true,
        finalized_at = COALESCE(finalized_at, created_at)
    WHERE date < ((CURRENT_TIMESTAMP AT TIME ZONE 'Asia/Shanghai')::date - 3);

    UPDATE dashboard_daily_stats
    SET is_finalized = false,
        finalized_at = NULL
    WHERE date >= ((CURRENT_TIMESTAMP AT TIME ZONE 'Asia/Shanghai')::date - 3);

    CREATE INDEX IF NOT EXISTS idx_dashboard_daily_stats_finalized_range
        ON dashboard_daily_stats (user_id, admin_account_id, date DESC)
        WHERE is_finalized;
END $$;
