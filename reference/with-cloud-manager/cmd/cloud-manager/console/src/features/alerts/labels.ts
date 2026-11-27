export const alertRules: Readonly<Partial<Record<string, string>>> = {
  operation_review: '操作待复核',
  operation_delayed: '操作超时',
  scan_failures: '连续盘点失败',
  scan_stale: '盘点数据过期',
  bucket_encryption_unconfigured: '对象桶未配置默认加密',
  bucket_versioning_inactive: '对象桶版本控制未启用',
  tag_noncompliant: '缺少必填标签',
  billing_budget_warning: '预算预警',
  billing_budget_exceeded: '达到预算额度',
}

export const bucketAlertFindings: Readonly<Partial<Record<string, string>>> = {
  bucket_encryption_unconfigured: 'encryption_unconfigured',
  bucket_versioning_inactive: 'versioning_inactive',
}
