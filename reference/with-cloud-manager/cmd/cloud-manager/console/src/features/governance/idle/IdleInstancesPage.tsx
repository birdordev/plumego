import { CollectionFilters } from '../../../shared/collection/CollectionFilters'
import { CollectionPage } from '../../../shared/collection/CollectionPage'
import { useCollection } from '../../../shared/collection/useCollection'
import { SelectField } from '../../../shared/components/SelectField'
import { idleCollection } from './collection'
import { IdleInstancesTable } from './IdleInstancesTable'

const options = [
  ['', '全部'], ['candidate', '低活动候选'], ['active', '观察到活动'], ['insufficient', '证据不足'], ['not_running', '未运行'],
] as const

export default function IdleInstancesPage() {
  const page = useCollection(idleCollection)
  if (!page) return null
  const filters = <CollectionFilters values={page.filters} onApply={idleCollection.applyFilters}>{(values) => <>
    <SelectField label="实例活动状态" name="state" initialValue={values.get('state')} options={options} />
    <label>账号<input name="account" defaultValue={values.get('account') ?? ''} maxLength={256} /></label>
    <label>地域<input name="region" defaultValue={values.get('region') ?? ''} maxLength={256} /></label>
  </>}</CollectionFilters>
  return <CollectionPage {...page} onLoadNext={idleCollection.loadNext} filters={filters}
    tools={<p className="hint">根据最近完成的七天 CPU 和网络采样识别人工核查候选。</p>}>
    <IdleInstancesTable items={page.items} />
  </CollectionPage>
}
