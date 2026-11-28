ALTER TABLE cmp_instance_console_grants
    ADD COLUMN provider text NOT NULL DEFAULT 'openstack',
    ADD COLUMN ssh_key_hash bytea,
    ADD COLUMN provider_request_id text,
    DROP CONSTRAINT cmp_instance_console_grants_status_check,
    ADD CONSTRAINT cmp_console_grant_status CHECK (status IN (
        'awaiting_approval','approved','rejected','revoked','in_use','closed','failed','key_sent','dispatch_unknown')),
    ADD CONSTRAINT cmp_console_grant_provider CHECK (
        (provider='openstack' AND ssh_key_hash IS NULL AND status NOT IN ('key_sent','dispatch_unknown'))
        OR (provider='aws-ec2' AND ssh_key_hash IS NOT NULL AND octet_length(ssh_key_hash)=32 AND status<>'closed')),
    ADD CONSTRAINT cmp_console_serial_receipt CHECK (
        (status='key_sent')=(provider_request_id IS NOT NULL)),
    ADD CONSTRAINT cmp_console_serial_review CHECK (
        status NOT IN ('key_sent','dispatch_unknown') OR reviewer_id IS NOT NULL);
