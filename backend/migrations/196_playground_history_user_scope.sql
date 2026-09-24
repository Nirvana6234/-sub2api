-- Playground history is one shared snapshot per user, independent of the
-- currently selected chat/image API key.
CREATE TABLE IF NOT EXISTS playground_histories_user_scope (
    user_id BIGINT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    state_payload JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

WITH source AS (
    SELECT user_id, state_payload, created_at, updated_at
    FROM playground_histories
    WHERE updated_at >= NOW() - INTERVAL '30 days'
), latest AS (
    SELECT DISTINCT ON (user_id)
        user_id,
        state_payload->'model' AS model,
        state_payload->'parameters' AS parameters,
        created_at,
        updated_at
    FROM source
    ORDER BY user_id, updated_at DESC
), projects AS (
    SELECT DISTINCT ON (source.user_id, project->>'id')
        source.user_id, project, source.updated_at
    FROM source
    CROSS JOIN LATERAL jsonb_array_elements(
        CASE WHEN jsonb_typeof(source.state_payload->'projects') = 'array'
             THEN source.state_payload->'projects' ELSE '[]'::jsonb END
    ) AS project
    ORDER BY source.user_id, project->>'id', source.updated_at DESC
), conversations AS (
    SELECT DISTINCT ON (source.user_id, conversation->>'id')
        source.user_id, conversation, source.updated_at
    FROM source
    CROSS JOIN LATERAL jsonb_array_elements(
        CASE WHEN jsonb_typeof(source.state_payload->'conversations') = 'array'
             THEN source.state_payload->'conversations' ELSE '[]'::jsonb END
    ) AS conversation
    ORDER BY source.user_id, conversation->>'id', source.updated_at DESC
), merged AS (
    SELECT
        latest.user_id,
        jsonb_build_object(
            'version', 2,
            'model', COALESCE(latest.model, '""'::jsonb),
            'parameters', COALESCE(latest.parameters, '{}'::jsonb),
            'activeConversationId', NULL,
            'projects', COALESCE((SELECT jsonb_agg(project ORDER BY updated_at DESC) FROM projects WHERE projects.user_id = latest.user_id), '[]'::jsonb),
            'conversations', COALESCE((SELECT jsonb_agg(conversation ORDER BY updated_at DESC) FROM conversations WHERE conversations.user_id = latest.user_id), '[]'::jsonb)
        ) AS state_payload,
        latest.created_at,
        latest.updated_at
    FROM latest
)
INSERT INTO playground_histories_user_scope (user_id, state_payload, created_at, updated_at)
SELECT user_id, state_payload, created_at, updated_at
FROM merged
ON CONFLICT (user_id) DO UPDATE
SET state_payload = EXCLUDED.state_payload,
    created_at = EXCLUDED.created_at,
    updated_at = EXCLUDED.updated_at;

DROP TABLE playground_histories;
ALTER TABLE playground_histories_user_scope RENAME TO playground_histories;
CREATE INDEX IF NOT EXISTS playground_histories_updated_at_idx
    ON playground_histories (updated_at DESC);
