ALTER TABLE cmp_instance_operations DROP CONSTRAINT cmp_instance_operations_status_check;
ALTER TABLE cmp_instance_operations ADD CONSTRAINT cmp_instance_operations_status_check
    CHECK (status IN ('awaiting_approval','queued','applying','observing','succeeded','failed','needs_review'));

DROP INDEX cmp_instance_active;
CREATE UNIQUE INDEX cmp_instance_active ON cmp_instance_operations (scope_id,external_id)
    WHERE status IN ('awaiting_approval','queued','applying','observing','needs_review');
DROP INDEX cmp_instance_active_project;
CREATE INDEX cmp_instance_active_project ON cmp_instance_operations (tenant_id,project_id)
    WHERE status IN ('awaiting_approval','queued','applying','observing','needs_review');

CREATE TABLE cmp_instance_approval_rules (
    tenant_id text NOT NULL,
    project_id text NOT NULL,
    action text NOT NULL CHECK (action IN ('start','stop','reboot')),
    PRIMARY KEY (tenant_id,project_id,action),
    FOREIGN KEY (tenant_id,project_id) REFERENCES cmp_projects
);

CREATE TABLE cmp_instance_operation_approvals (
    operation_id text PRIMARY KEY REFERENCES cmp_instance_operations,
    tenant_id text NOT NULL,
    reviewer_id text NOT NULL,
    decision text NOT NULL CHECK (decision IN ('approve','reject')),
    decided_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    FOREIGN KEY (tenant_id,reviewer_id) REFERENCES cmp_principals
);
