CREATE TABLE cmp_account_operation_rollouts (
    tenant_id text NOT NULL,
    account_id text NOT NULL,
    revision bigint NOT NULL CHECK (revision > 0),
    rules jsonb NOT NULL CHECK (jsonb_typeof(rules) = 'array'),
    PRIMARY KEY (tenant_id, account_id, revision),
    FOREIGN KEY (tenant_id, account_id) REFERENCES cmp_cloud_accounts
);

ALTER TABLE cmp_instance_operations
    ADD COLUMN rollout_revisions bigint[] NOT NULL DEFAULT ARRAY[0,0]::bigint[];

CREATE FUNCTION cmp_account_operation_rollout_revision(
    tenant text, account text, region text, action text
) RETURNS bigint LANGUAGE sql STABLE AS $$
    SELECT COALESCE((
        SELECT CASE WHEN p.rules @> jsonb_build_array(jsonb_build_object(
            'region', $3, 'action', $4)) THEN p.revision ELSE -1 END
        FROM cmp_account_operation_rollouts p
        WHERE (p.tenant_id,p.account_id)=($1,$2)
        ORDER BY p.revision DESC LIMIT 1
    ),0)
$$;

CREATE FUNCTION cmp_operation_rollout_revisions(op cmp_instance_operations)
RETURNS bigint[] LANGUAGE plpgsql AS $$
DECLARE
    source_account text;
    source_region text;
    target_revision bigint;
    source_revision bigint := 0;
BEGIN
    IF op.action='copy-image' THEN
        SELECT i.source_account,i.source_region INTO STRICT source_account,source_region
        FROM cmp_image_copy_intents i WHERE i.operation_id=op.operation_id;
    END IF;

    -- 管理端持有同一账号行的更新锁。拿到锁后再读最新策略。
    PERFORM a.account_id FROM cmp_cloud_accounts a
    WHERE a.tenant_id=op.tenant_id AND a.account_id IN (op.account_id,source_account)
    ORDER BY a.account_id FOR SHARE;

    target_revision := cmp_account_operation_rollout_revision(op.tenant_id,op.account_id,op.region,op.action);
    IF source_account IS NOT NULL THEN
        source_revision := cmp_account_operation_rollout_revision(op.tenant_id,source_account,source_region,op.action);
    END IF;
    RETURN ARRAY[target_revision,source_revision];
END;
$$;

CREATE FUNCTION cmp_guard_operation_rollout_dispatch()
RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    current_revisions bigint[];
BEGIN
    current_revisions := cmp_operation_rollout_revisions(OLD);
    IF -1=ANY(current_revisions) OR current_revisions<>OLD.rollout_revisions THEN
        RAISE EXCEPTION 'operation dispatch requires an active rollout revision'
            USING ERRCODE='23514', CONSTRAINT='cmp_operation_rollout_dispatch';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER cmp_operation_rollout_dispatch BEFORE UPDATE OF status ON cmp_instance_operations
    FOR EACH ROW WHEN (OLD.status='queued' AND NEW.status='applying')
    EXECUTE FUNCTION cmp_guard_operation_rollout_dispatch();
