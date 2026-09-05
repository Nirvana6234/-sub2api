-- Advertise the locally built v0.2 client without overwriting administrator overrides.
-- The download host serves immutable, versioned filenames, so the URL changes too.

UPDATE settings
SET value = 'https://download.gongfeiai.com/downloads/codex-relay-client_v0.2_x64.zip',
    updated_at = NOW()
WHERE key = 'client_download_direct_url'
  AND value IN (
    'https://download.gongfeiai.com/downloads/codex-relay-client_v0.1.2_x64.zip',
    'https://icode-xtu.cc.cd/downloads/codex-relay-client_v0.1.1_x64.zip',
    'https://icode-xtu.cc.cd/downloads/codex-relay-client_v0.1.2_x64.zip'
  );

UPDATE settings
SET value = '0.2',
    updated_at = NOW()
WHERE key = 'client_latest_version'
  AND COALESCE(TRIM(value), '') = '';
