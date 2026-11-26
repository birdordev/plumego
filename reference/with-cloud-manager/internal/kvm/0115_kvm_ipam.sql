CREATE TABLE cmp_kvm_ipv4_pools (
    tenant_id text NOT NULL,
    project_id text NOT NULL,
    site_id text NOT NULL,
    network_id text NOT NULL,
    subnet cidr NOT NULL,
    gateway inet NOT NULL,
    first_ip inet NOT NULL,
    last_ip inet NOT NULL,
    created_by text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (tenant_id,project_id,site_id,network_id),
    FOREIGN KEY (tenant_id,project_id) REFERENCES cmp_projects,
    FOREIGN KEY (tenant_id,project_id,created_by) REFERENCES cmp_memberships,
    CHECK (family(subnet)=4 AND masklen(subnet) BETWEEN 16 AND 30),
    CHECK (masklen(gateway)=32 AND masklen(first_ip)=32 AND masklen(last_ip)=32),
    CHECK (gateway <<= subnet AND first_ip <<= subnet AND last_ip <<= subnet),
    CHECK (first_ip>host(network(subnet))::inet AND last_ip<host(broadcast(subnet))::inet),
    CHECK (gateway>host(network(subnet))::inet AND gateway<host(broadcast(subnet))::inet),
    CHECK (last_ip-first_ip BETWEEN 0 AND 4095 AND (gateway<first_ip OR gateway>last_ip))
);

ALTER TABLE cmp_kvm_placements
    ADD COLUMN ipam_network_id text,
    ADD COLUMN ipv4 inet,
    ADD CONSTRAINT cmp_kvm_placement_ipv4_pool FOREIGN KEY (tenant_id,project_id,site_id,ipam_network_id) REFERENCES cmp_kvm_ipv4_pools,
    ADD CONSTRAINT cmp_kvm_placement_ipv4_shape CHECK (
        (ipv4 IS NULL AND ipam_network_id IS NULL) OR
        (ipv4 IS NOT NULL AND ipam_network_id IS NOT NULL AND ipam_network_id=network_id AND family(ipv4)=4 AND masklen(ipv4)=32));

CREATE UNIQUE INDEX cmp_kvm_bound_ipv4 ON cmp_kvm_placements (tenant_id,project_id,site_id,ipam_network_id,ipv4)
    WHERE ipv4 IS NOT NULL AND released_at IS NULL AND expired_at IS NULL;

CREATE INDEX cmp_kvm_active_network_holds ON cmp_kvm_placements (tenant_id,project_id,site_id,network_id)
    WHERE released_at IS NULL AND expired_at IS NULL;
