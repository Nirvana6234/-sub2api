-- Existing installations may not have the version setting because it was added
-- after their initial settings seed. Create it without overriding an admin value.
INSERT INTO settings (key, value, updated_at)
VALUES ('client_latest_version', '0.2', NOW())
ON CONFLICT (key) DO UPDATE
SET value = CASE
    WHEN COALESCE(TRIM(settings.value), '') = '' THEN EXCLUDED.value
    ELSE settings.value
  END,
  updated_at = CASE
    WHEN COALESCE(TRIM(settings.value), '') = '' THEN EXCLUDED.updated_at
    ELSE settings.updated_at
  END;
