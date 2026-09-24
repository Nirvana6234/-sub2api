-- Preserve legacy automatic keys whose candidate list was never configured,
-- then remove soft-deleted groups from persisted candidate lists. New empty
-- lists are reserved for the explicit "no candidates remain" state.
UPDATE api_keys AS k
SET auto_group_ids = COALESCE(
    (
        SELECT jsonb_agg(g.id ORDER BY g.id)
        FROM groups AS g
        WHERE g.deleted_at IS NULL
          AND g.status = 'active'
    ),
    '[]'::jsonb
)
WHERE k.auto_group = TRUE
  AND k.deleted_at IS NULL
  AND k.auto_group_ids = '[]'::jsonb;

UPDATE api_keys AS k
SET auto_group_ids = COALESCE(
    (
        SELECT jsonb_agg(item.value ORDER BY item.ordinality)
        FROM jsonb_array_elements(k.auto_group_ids) WITH ORDINALITY AS item(value, ordinality)
        WHERE EXISTS (
            SELECT 1
            FROM groups AS g
            WHERE g.id = (item.value #>> '{}')::bigint
              AND g.deleted_at IS NULL
        )
    ),
    '[]'::jsonb
)
WHERE k.auto_group = TRUE
  AND k.deleted_at IS NULL
  AND k.auto_group_ids <> '[]'::jsonb;
