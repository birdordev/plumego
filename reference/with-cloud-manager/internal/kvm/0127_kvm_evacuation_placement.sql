ALTER TABLE cmp_kvm_cold_migrations
    ADD COLUMN automatic_placement boolean NOT NULL DEFAULT false,
    ADD CHECK (NOT automatic_placement OR source_fence_generation>0);
