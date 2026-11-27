import { SelectField } from '../../../shared/components/SelectField'

const statuses = [
  ['', '全部状态'], ['awaiting_approval', '待审批'], ['queued', '已排队'], ['applying', '执行中'],
  ['observing', '观测中'], ['needs_review', '待复核'], ['succeeded', '成功'], ['failed', '失败'],
] as const

export function OperationFilters({ values }: { values: URLSearchParams }) {
  return <>
    <label>账号<input name="account" defaultValue={values.get('account') ?? ''} maxLength={128} /></label>
    <SelectField label="状态" name="status" initialValue={values.get('status')} options={statuses} />
  </>
}
