ALTER TABLE cmp_cloud_account_events
    DROP CONSTRAINT cmp_cloud_account_events_action_check,
    ADD CONSTRAINT cmp_cloud_account_events_action_check CHECK (
        action IN ('register','rotate','suspend','resume','transfer','share','unshare',
                   'enable-bare-metal-power','disable-bare-metal-power','enable-write','disable-write')) NOT VALID;
