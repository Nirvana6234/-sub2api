-- STATE Kit owns the OpenAI OAuth outbound transport. Remove the previous
-- account-level OAuth passthrough switches so an old setting cannot compete
-- with the plugin. API Key passthrough remains unchanged.
UPDATE accounts
SET extra = extra - 'openai_passthrough' - 'openai_oauth_passthrough',
    updated_at = NOW()
WHERE platform = 'openai'
  AND type IN ('oauth', 'setup-token')
  AND (
      extra ? 'openai_passthrough'
      OR extra ? 'openai_oauth_passthrough'
  );
