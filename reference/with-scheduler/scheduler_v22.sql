ALTER TABLE schedules ADD COLUMN zone_rules bytea NOT NULL DEFAULT ''::bytea
    CHECK (octet_length(zone_rules) <= 65536);
