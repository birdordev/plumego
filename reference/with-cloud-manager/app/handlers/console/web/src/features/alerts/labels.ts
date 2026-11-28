export const alertRules: Readonly<Partial<Record<string, string>>> = {
  operation_review: '操作待复核',
  operation_delayed: '操作超时',
  operation_queue_recovery: '租约接管积压',
  operation_queue_anomaly: '操作队列异常',
  scan_failures: '连续盘点失败',
  scan_stale: '盘点数据过期',
  bucket_encryption_unconfigured: '对象桶未配置默认加密',
  bucket_versioning_inactive: '对象桶版本控制未启用',
  database_backup_coverage: '数据库备份保障不足',
  database_restore_coverage: '数据库恢复窗口保障不足',
  tag_noncompliant: '缺少必填标签',
  billing_budget_warning: '预算预警',
  billing_budget_exceeded: '达到预算额度',
}

export const databaseAlertFindings: Readonly<Partial<Record<string, string>>> = {
  database_backup_coverage: 'backup_attention',
  database_restore_coverage: 'recovery_attention',
}

export const bucketAlertFindings: Readonly<Partial<Record<string, string>>> = {
  bucket_encryption_unconfigured: 'encryption_unconfigured',
  bucket_versioning_inactive: 'versioning_inactive',
}

export const queueAlertSources: Readonly<Partial<Record<string, string>>> = {
  operation_queue_recovery: '到期待接管',
  operation_queue_anomaly: '活动操作的队列记录异常',
}
