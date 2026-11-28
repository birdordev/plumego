import { CollectionFilters } from '../../../shared/collection/CollectionFilters'
import { SelectField } from '../../../shared/components/SelectField'
import { providerOptions } from '../../../shared/display/providers'
import { dispatchOptions } from './DispatchStatus'
import { freshnessStates } from './labels'

export function FreshnessFilters({ filters, apply }: { filters: string; apply: (filters: string) => void }) {
  return <CollectionFilters values={filters} onApply={apply}>{(values) => <>
    <SelectField label="云厂商" name="provider" initialValue={values.get('provider')} options={[[ '', '全部厂商' ], ...providerOptions]} />
    <label>账号<input name="account" defaultValue={values.get('account') ?? ''} maxLength={128} /></label>
    <SelectField label="新鲜度状态" name="state" initialValue={values.get('state')} options={[[ '', '全部状态' ], ...freshnessStates]} />
    <SelectField label="调度状态" name="dispatch_state" initialValue={values.get('dispatch_state')} options={[[ '', '全部调度状态' ], ...dispatchOptions]} />
    <SelectField label="明细排序" name="order" initialValue={values.get('order')} options={[[ '', '计划 ID' ], [ 'next_at', '最早计划优先' ]]} />
    <label>新鲜度窗口（秒）<input type="number" name="window_seconds" required min={60} max={604800}
      defaultValue={values.get('window_seconds') ?? 900} /></label>
  </>}</CollectionFilters>
}
