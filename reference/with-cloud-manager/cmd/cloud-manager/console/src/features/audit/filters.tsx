import { SelectField } from '../../shared/components/SelectField'
import { localDateTime } from '../../shared/display/time'

const categories = [
  ['', '全部事件'], ['account', '云账号'], ['ownership', '资产归属'], ['operation', '操作状态'],
  ['approval', '审批'], ['resolution', '人工处置'], ['observation', '续查'], ['catalog', '服务目录'],
  ['alert', '项目告警'], ['billing_budget', '云预算'], ['tag_policy', '标签规则'], ['change_ticket', '变更工单'], ['console_access', '原生控制台'],
] as const

export function AuditFilters({ values }: { values: URLSearchParams }) {
  return <>
    <SelectField label="事件类型" name="category" initialValue={values.get('category')} options={categories} />
    <label>账号<input name="account" defaultValue={values.get('account') ?? ''} maxLength={128} /></label>
    <label>对象 ID<input name="subject_id" defaultValue={values.get('subject_id') ?? ''} maxLength={512} /></label>
    <label>开始时间<input name="from" type="datetime-local" step="any" defaultValue={localDateTime(values.get('from'))} /></label>
    <label>截止时间<input name="until" type="datetime-local" step="any" defaultValue={localDateTime(values.get('until'))} /></label>
  </>
}
