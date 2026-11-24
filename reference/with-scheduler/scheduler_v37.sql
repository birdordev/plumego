CREATE TABLE scheduler_business_calendars (
    tenant_id text NOT NULL,
    name text NOT NULL,
    version integer NOT NULL CHECK (version > 0),
    time_zone text NOT NULL,
    zone_rules bytea NOT NULL,
    open_days integer NOT NULL CHECK (open_days BETWEEN 1 AND 127),
    exceptions jsonb NOT NULL,
    PRIMARY KEY (tenant_id, name, version)
);

ALTER TABLE schedules
    ADD COLUMN calendar_name text,
    ADD COLUMN calendar_version integer,
    ADD CHECK ((calendar_name IS NULL) = (calendar_version IS NULL)),
    ADD FOREIGN KEY (tenant_id, calendar_name, calendar_version)
        REFERENCES scheduler_business_calendars (tenant_id, name, version);

ALTER TABLE scheduled_runs
    ADD COLUMN calendar_name text,
    ADD COLUMN calendar_version integer,
    ADD CHECK ((calendar_name IS NULL) = (calendar_version IS NULL)),
    ADD FOREIGN KEY (tenant_id, calendar_name, calendar_version)
        REFERENCES scheduler_business_calendars (tenant_id, name, version);
