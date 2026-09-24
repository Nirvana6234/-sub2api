-- User-managed proxies are private to the contributor who created them.
-- Administrator-managed proxies keep owner_user_id NULL and remain in the
-- existing global proxy pool.
ALTER TABLE proxies
    ADD COLUMN IF NOT EXISTS owner_user_id BIGINT;

CREATE INDEX IF NOT EXISTS idx_proxies_owner_user_id
    ON proxies(owner_user_id)
    WHERE deleted_at IS NULL;
