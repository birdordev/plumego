CREATE INDEX scheduler_scale_arrivals ON mq_tasks (topic, created_at);
CREATE INDEX scheduler_scale_completions ON mq_task_attempts (finished_at, task_id)
    WHERE execution_no IS NOT NULL AND finished_at IS NOT NULL;
CREATE INDEX scheduler_scale_starts ON mq_task_attempts (execution_started_at)
    WHERE execution_started_at IS NOT NULL;
