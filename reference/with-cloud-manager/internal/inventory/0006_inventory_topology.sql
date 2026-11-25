ALTER TABLE cmp_inventory_objects
    ADD COLUMN links jsonb NOT NULL DEFAULT '[]'::jsonb
    CHECK (jsonb_typeof(links)='array' AND jsonb_array_length(links)<=256);
