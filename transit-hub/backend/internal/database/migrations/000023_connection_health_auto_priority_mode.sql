-- Preserve the behavior of health-probe multiplier policies while naming the
-- Sub2API-style normalized scoring mode explicitly in the UI and API.
UPDATE connection_health_policies
SET priority_mode = 'auto'
WHERE priority_mode = 'multiplier'
  AND strategy_mode <> 'multiplier_only';
