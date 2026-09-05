ALTER TABLE IF EXISTS connection_health_policies
ADD COLUMN IF NOT EXISTS priority_strategy text NOT NULL DEFAULT 'price';

DO $$
BEGIN
    IF to_regclass('public.connection_health_policies') IS NOT NULL THEN
        UPDATE connection_health_policies
        SET priority_strategy = 'price'
        WHERE priority_strategy NOT IN ('price', 'balanced', 'speed');
    END IF;
END
$$;
