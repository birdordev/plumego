import { SelectField } from '../../shared/components/SelectField'

const states = [
  ['active', '未恢复'], ['open', '待确认'], ['acknowledged', '已确认'], ['resolved', '已恢复'], ['all', '全部'],
] as const

const rules = [
  ['', '全部规则'], ['operation_review', '操作待复核'], ['operation_delayed', '操作超时'],
  ['operation_queue_recovery', '租约接管积压'], ['operation_queue_anomaly', '操作队列异常'],
  ['scan_failures', '连续盘点失败'], ['scan_stale', '盘点数据过期'],
  ['bucket_encryption_unconfigured', '对象桶未配置默认加密'], ['bucket_versioning_inactive', '对象桶版本控制未启用'],
  ['database_backup_coverage', '数据库备份保障不足'], ['database_restore_coverage', '数据库恢复窗口保障不足'],
  ['tag_noncompliant', '缺少必填标签'],
] as const

const billingRules = [
  ['billing_budget_warning', '预算预警'], ['billing_budget_exceeded', '达到预算额度'],
] as const

export function AlertFilters({ admin, values }: { admin: boolean; values: URLSearchParams }) {
  return <>
    <SelectField label="告警状态" name="status" initialValue={values.get('status')} options={states} />
    <SelectField label="告警规则" name="rule" initialValue={values.get('rule')} options={admin ? [...rules, ...billingRules] : rules} />
  </>
}
