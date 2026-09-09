-- Seed the client-download tutorial video URL for existing installations, whose
-- settings table was already initialized before this key existed (the in-code
-- default map in InitializeDefaultSettings only applies to brand-new installs).
INSERT INTO settings (key, value, updated_at)
VALUES ('client_tutorial_video_url', 'https://www.bilibili.com/video/BV1vWYJ6PEhc/', NOW())
ON CONFLICT (key) DO UPDATE
SET value = CASE
    WHEN COALESCE(TRIM(settings.value), '') = '' THEN EXCLUDED.value
    ELSE settings.value
  END,
  updated_at = CASE
    WHEN COALESCE(TRIM(settings.value), '') = '' THEN EXCLUDED.updated_at
    ELSE settings.updated_at
  END;
