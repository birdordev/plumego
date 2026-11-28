SET LOCAL lock_timeout = '2s';

ALTER TABLE cmp_instance_observation_retries ADD COLUMN repair_queue boolean NOT NULL DEFAULT false;
