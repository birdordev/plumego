ALTER TABLE cmp_instance_launches ADD COLUMN provider_instance_id text NOT NULL DEFAULT '';

CREATE UNIQUE INDEX cmp_instance_creation_asset ON cmp_instance_operations (scope_id,external_id)
    WHERE action='create' AND scope_id IS NOT NULL;

ALTER TABLE cmp_instance_launches
    ADD COLUMN observation_reviewer text NOT NULL DEFAULT '',
    ADD COLUMN observation_membership_revision bigint NOT NULL DEFAULT 0,
    ADD COLUMN observation_credential_revision bigint NOT NULL DEFAULT 0,
    ADD CONSTRAINT cmp_creation_observation_authority CHECK (
        (observation_reviewer='' AND observation_membership_revision=0 AND observation_credential_revision=0)
        OR (observation_reviewer<>'' AND observation_membership_revision>0 AND observation_credential_revision>0)
    );

ALTER TABLE cmp_instance_observation_retries
    ADD COLUMN membership_revision bigint CHECK (membership_revision > 0),
    ADD COLUMN credential_revision bigint CHECK (credential_revision > 0);
