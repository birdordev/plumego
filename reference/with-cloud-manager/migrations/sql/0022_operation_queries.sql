CREATE INDEX cmp_project_operations_recent
    ON cmp_instance_operations (tenant_id,project_id,created_at DESC,operation_id DESC);
CREATE INDEX cmp_project_operations_status_recent
    ON cmp_instance_operations (tenant_id,project_id,status,created_at DESC,operation_id DESC);
