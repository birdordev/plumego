ALTER TABLE cmp_inventory_objects
    ADD COLUMN tag_keys text[],
    ADD COLUMN tag_observed_at timestamptz,
    ADD COLUMN tag_credential_revision bigint,
    ADD CONSTRAINT cmp_inventory_tag_observation_complete CHECK (
        (tag_keys IS NULL AND tag_observed_at IS NULL AND tag_credential_revision IS NULL)
        OR (tag_keys IS NOT NULL AND tag_observed_at IS NOT NULL
            AND tag_credential_revision IS NOT NULL AND tag_credential_revision>0)) NOT VALID;
