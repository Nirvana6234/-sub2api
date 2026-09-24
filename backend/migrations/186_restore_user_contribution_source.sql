-- Keep account ownership and credential import method in separate fields.
-- Earlier contribution flows overwrote import_source after writing the owner,
-- which made valid user accounts disappear from the contributor's account list.
UPDATE accounts
SET extra = jsonb_set(
    jsonb_set(
        COALESCE(extra, '{}'::jsonb),
        '{contribution_import_method}',
        to_jsonb(extra->>'import_source'),
        true
    ),
    '{import_source}',
    '"user_contribution"'::jsonb,
    true
)
WHERE extra ? 'submitted_by_user_id'
  AND NULLIF(BTRIM(extra->>'submitted_by_user_id'), '') IS NOT NULL
  AND NULLIF(BTRIM(extra->>'import_source'), '') IS NOT NULL
  AND extra->>'import_source' <> 'user_contribution';
