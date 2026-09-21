-- Publish the v0.5 client without overwriting administrator-customized links.
-- Existing v0.4 values are the previous built-in release and can be moved to
-- the new immutable filenames safely. Blank values are also initialized so an
-- older installation does not keep pointing at a dead download.
UPDATE settings
SET value = 'https://download.gongfeiai.com/downloads/codex-relay-client_v0.5_x64.zip',
    updated_at = NOW()
WHERE key = 'client_download_direct_url'
  AND (
    COALESCE(TRIM(value), '') = ''
    OR value LIKE '%codex-relay-client_v0.4_x64.zip'
  );

UPDATE settings
SET value = 'https://download.gongfeiai.com/downloads/codex-relay-client_v0.5_macos-arm64.tar.gz',
    updated_at = NOW()
WHERE key = 'client_download_direct_url_mac'
  AND (
    COALESCE(TRIM(value), '') = ''
    OR value LIKE '%codex-relay-client_v0.4_macos-arm64.tar.gz'
  );

UPDATE settings
SET value = '0.5',
    updated_at = NOW()
WHERE key IN ('client_latest_version', 'client_latest_version_mac')
  AND COALESCE(TRIM(value), '') IN ('', '0.4');

INSERT INTO settings (key, value, updated_at)
SELECT key, value, NOW()
FROM (VALUES
  ('client_download_direct_url', 'https://download.gongfeiai.com/downloads/codex-relay-client_v0.5_x64.zip'),
  ('client_download_direct_url_mac', 'https://download.gongfeiai.com/downloads/codex-relay-client_v0.5_macos-arm64.tar.gz'),
  ('client_latest_version', '0.5'),
  ('client_latest_version_mac', '0.5')
) AS defaults(key, value)
WHERE NOT EXISTS (SELECT 1 FROM settings WHERE settings.key = defaults.key);
