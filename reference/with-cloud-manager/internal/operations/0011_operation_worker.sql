ALTER TABLE cmp_instance_operations ADD COLUMN provider_request_id text;
ALTER TABLE cmp_instance_operations ADD COLUMN outcome_reason text;
ALTER TABLE cmp_instance_operations ADD COLUMN updated_at timestamptz;
