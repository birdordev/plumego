CREATE TABLE scheduler_workflow_definitions (
    tenant_id text NOT NULL,
    name text NOT NULL,
    version integer NOT NULL CHECK (version > 0),
    graph jsonb NOT NULL CHECK (jsonb_typeof(graph) = 'object'),
    PRIMARY KEY (tenant_id,name,version)
);

CREATE TABLE scheduler_workflow_releases (
    tenant_id text NOT NULL,
    name text NOT NULL,
    active_version integer NOT NULL,
    PRIMARY KEY (tenant_id,name),
    FOREIGN KEY (tenant_id,name,active_version) REFERENCES scheduler_workflow_definitions (tenant_id,name,version)
);

CREATE TABLE scheduler_workflow_release_commands (
    tenant_id text NOT NULL,
    name text NOT NULL,
    request_key text NOT NULL,
    version integer NOT NULL,
    expected_version integer NOT NULL CHECK (expected_version >= 0),
    PRIMARY KEY (tenant_id,name,request_key),
    FOREIGN KEY (tenant_id,name,version) REFERENCES scheduler_workflow_definitions (tenant_id,name,version)
);

ALTER TABLE scheduler_workflows
    ADD COLUMN definition_name text,
    ADD COLUMN definition_version integer,
    ADD CHECK ((definition_name IS NULL) = (definition_version IS NULL)),
    ADD FOREIGN KEY (tenant_id,definition_name,definition_version)
        REFERENCES scheduler_workflow_definitions (tenant_id,name,version);
