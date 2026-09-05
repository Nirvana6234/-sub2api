-- Durable Auto-group selection invalidation reuses the auth invalidation
-- outbox. Scope messages contain only numeric user/group IDs and are safe to
-- publish through the existing cross-instance channel.

ALTER TABLE auth_cache_invalidation_outbox
    ALTER COLUMN cache_key TYPE TEXT;

ALTER TABLE auth_cache_invalidation_outbox
    DROP CONSTRAINT IF EXISTS auth_cache_invalidation_outbox_cache_key_check;

ALTER TABLE auth_cache_invalidation_outbox
    ADD CONSTRAINT auth_cache_invalidation_outbox_cache_key_check
    CHECK (
        cache_key ~ '^[0-9a-f]{64}$'
        OR cache_key ~ '^auto-group:(user|group):[1-9][0-9]*$'
    );

CREATE OR REPLACE FUNCTION enqueue_auto_group_scope_invalidation(scope_name TEXT, target_id BIGINT)
RETURNS VOID
LANGUAGE plpgsql
AS $$
BEGIN
    IF scope_name NOT IN ('user', 'group') OR target_id IS NULL OR target_id <= 0 THEN
        RETURN;
    END IF;
    INSERT INTO auth_cache_invalidation_outbox (cache_key)
    VALUES ('auto-group:' || scope_name || ':' || target_id::TEXT);
END;
$$;

CREATE OR REPLACE FUNCTION enqueue_group_auto_group_invalidation()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP = 'UPDATE'
       AND OLD.rate_multiplier IS NOT DISTINCT FROM NEW.rate_multiplier
       AND OLD.status IS NOT DISTINCT FROM NEW.status
       AND OLD.is_exclusive IS NOT DISTINCT FROM NEW.is_exclusive
       AND OLD.platform IS NOT DISTINCT FROM NEW.platform
       AND OLD.sort_order IS NOT DISTINCT FROM NEW.sort_order
       AND OLD.deleted_at IS NOT DISTINCT FROM NEW.deleted_at THEN
        RETURN NEW;
    END IF;
    PERFORM enqueue_auto_group_scope_invalidation('group', OLD.id);
    IF TG_OP = 'DELETE' THEN
        RETURN OLD;
    END IF;
    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS trg_groups_auto_group_invalidation ON groups;
CREATE TRIGGER trg_groups_auto_group_invalidation
AFTER UPDATE OR DELETE ON groups
FOR EACH ROW EXECUTE FUNCTION enqueue_group_auto_group_invalidation();

CREATE OR REPLACE FUNCTION enqueue_user_group_rate_auto_group_invalidation()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP = 'UPDATE'
       AND OLD.user_id IS NOT DISTINCT FROM NEW.user_id
       AND OLD.group_id IS NOT DISTINCT FROM NEW.group_id
       AND OLD.rate_multiplier IS NOT DISTINCT FROM NEW.rate_multiplier THEN
        RETURN NEW;
    END IF;
    IF TG_OP IN ('UPDATE', 'DELETE') THEN
        PERFORM enqueue_auto_group_scope_invalidation('user', OLD.user_id);
    END IF;
    IF TG_OP IN ('UPDATE', 'INSERT')
       AND (TG_OP = 'INSERT' OR NEW.user_id IS DISTINCT FROM OLD.user_id) THEN
        PERFORM enqueue_auto_group_scope_invalidation('user', NEW.user_id);
    END IF;
    IF TG_OP = 'DELETE' THEN
        RETURN OLD;
    END IF;
    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS trg_user_group_rates_auto_group_invalidation ON user_group_rate_multipliers;
CREATE TRIGGER trg_user_group_rates_auto_group_invalidation
AFTER INSERT OR UPDATE OR DELETE ON user_group_rate_multipliers
FOR EACH ROW EXECUTE FUNCTION enqueue_user_group_rate_auto_group_invalidation();

CREATE OR REPLACE FUNCTION enqueue_api_key_auto_group_invalidation()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP = 'UPDATE'
       AND OLD.user_id IS NOT DISTINCT FROM NEW.user_id
       AND OLD.auto_group IS NOT DISTINCT FROM NEW.auto_group
       AND OLD.auto_group_strategy IS NOT DISTINCT FROM NEW.auto_group_strategy
       AND OLD.auto_group_ids IS NOT DISTINCT FROM NEW.auto_group_ids
       AND OLD.deleted_at IS NOT DISTINCT FROM NEW.deleted_at THEN
        RETURN NEW;
    END IF;
    IF TG_OP IN ('UPDATE', 'DELETE') THEN
        PERFORM enqueue_auto_group_scope_invalidation('user', OLD.user_id);
    END IF;
    IF TG_OP = 'UPDATE' AND NEW.user_id IS DISTINCT FROM OLD.user_id THEN
        PERFORM enqueue_auto_group_scope_invalidation('user', NEW.user_id);
    END IF;
    IF TG_OP = 'DELETE' THEN
        RETURN OLD;
    END IF;
    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS trg_api_keys_auto_group_invalidation ON api_keys;
CREATE TRIGGER trg_api_keys_auto_group_invalidation
AFTER UPDATE OR DELETE ON api_keys
FOR EACH ROW EXECUTE FUNCTION enqueue_api_key_auto_group_invalidation();

CREATE OR REPLACE FUNCTION enqueue_allowed_group_auto_group_invalidation()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP IN ('UPDATE', 'DELETE') THEN
        PERFORM enqueue_auto_group_scope_invalidation('user', OLD.user_id);
    END IF;
    IF TG_OP IN ('UPDATE', 'INSERT')
       AND (TG_OP = 'INSERT' OR NEW.user_id IS DISTINCT FROM OLD.user_id) THEN
        PERFORM enqueue_auto_group_scope_invalidation('user', NEW.user_id);
    END IF;
    IF TG_OP = 'DELETE' THEN
        RETURN OLD;
    END IF;
    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS trg_user_allowed_groups_auto_group_invalidation ON user_allowed_groups;
CREATE TRIGGER trg_user_allowed_groups_auto_group_invalidation
AFTER INSERT OR UPDATE OR DELETE ON user_allowed_groups
FOR EACH ROW EXECUTE FUNCTION enqueue_allowed_group_auto_group_invalidation();

COMMENT ON CONSTRAINT auth_cache_invalidation_outbox_cache_key_check
ON auth_cache_invalidation_outbox IS
    'Allows SHA-256 auth cache keys and non-secret Auto-group user/group scope messages';
