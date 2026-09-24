-- Rebrand only the upstream defaults. Administrator-defined site names,
-- subtitles, and logos are intentionally left untouched.
UPDATE settings
SET value = '共飞 AI', updated_at = NOW()
WHERE key = 'site_name' AND value = 'Sub2API';

UPDATE settings
SET value = '一起共享，一起使用 AI', updated_at = NOW()
WHERE key = 'site_subtitle' AND value = 'Subscription to API Conversion Platform';
