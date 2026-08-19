-- User-contributed accounts are private by default. Sharing is represented
-- only by an enabled, verified contribution-room membership.
UPDATE accounts
SET extra = jsonb_set(
    COALESCE(extra, '{}'::jsonb) - ARRAY[
        'share_total_budget',
        'share_daily_budget',
        'share_expires_at',
        'share_used_total',
        'share_used_today',
        'share_usage_day',
        'share_consumer_rate_multiplier'
    ],
    '{share_mode}',
    '"private"'::jsonb,
    true
)
WHERE extra->>'import_source' = 'user_contribution';
