-- 让待领取和租约失效分支分别按 Topic 和时间缩小候选范围。
CREATE INDEX mq_tasks_reclaim_topic ON mq_tasks (topic, lease_until)
    WHERE status = 'leased';

-- 清理固定按到期时间和 ID 前进，两个状态分区分别限量。
CREATE INDEX mq_tasks_expire_queued_order ON mq_tasks (expires_at, id)
    WHERE status = 'queued' AND expires_at IS NOT NULL;
CREATE INDEX mq_tasks_expire_leased_order ON mq_tasks (expires_at, id)
    WHERE status = 'leased' AND expires_at IS NOT NULL;
