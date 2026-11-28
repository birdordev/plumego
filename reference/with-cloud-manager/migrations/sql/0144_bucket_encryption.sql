CREATE TABLE cmp_bucket_encryption (
    scope_id bigint NOT NULL,
    external_id text NOT NULL,
    credential_revision bigint NOT NULL CHECK (credential_revision > 0),
    location text NOT NULL,
    state text NOT NULL CHECK (state IN ('unconfigured','aes256','sm4','kms')),
    refresh_started_at timestamptz NOT NULL,
    observed_at timestamptz NOT NULL,
    PRIMARY KEY (scope_id,external_id),
    FOREIGN KEY (scope_id,external_id) REFERENCES cmp_inventory_objects ON DELETE CASCADE
);
