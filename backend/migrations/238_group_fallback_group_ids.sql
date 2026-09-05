-- Allow each source group to try multiple fallback pools in priority order.
-- Keep fallback_group_id as the legacy first target for older clients.
ALTER TABLE groups
    ADD COLUMN IF NOT EXISTS fallback_group_ids JSONB NOT NULL DEFAULT '[]'::jsonb;

UPDATE groups
SET fallback_group_ids = jsonb_build_array(fallback_group_id)
WHERE fallback_group_id IS NOT NULL
  AND jsonb_typeof(fallback_group_ids) = 'array'
  AND jsonb_array_length(fallback_group_ids) = 0;

CREATE INDEX IF NOT EXISTS idx_groups_fallback_group_ids
    ON groups USING GIN (fallback_group_ids)
    WHERE deleted_at IS NULL;
