export const alertRules: Readonly<Partial<Record<string, string>>> = {
  operation_review: '操作待复核',
  operation_delayed: '操作超时',
  scan_failures: '连续盘点失败',
  scan_stale: '盘点数据过期',
  billing_budget_warning: '预算预警',
  billing_budget_exceeded: '达到预算额度',
}
