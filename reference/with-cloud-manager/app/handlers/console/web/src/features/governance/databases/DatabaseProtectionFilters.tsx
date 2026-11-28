import { CollectionFilters } from '../../../shared/collection/CollectionFilters'
import { SelectField } from '../../../shared/components/SelectField'

const findings = [
  ['', '全部数据库'],
  ['backup_attention', '备份需关注'],
  ['recovery_attention', '恢复窗口需关注'],
  ['inventory_unavailable', '资产盘点待确认'],
  ['refresh_failed', '启用计划刷新异常'],
] as const

export function DatabaseProtectionFilters({ filters, apply }: { filters: string; apply: (input: string) => void }) {
  return <CollectionFilters values={filters} onApply={apply}>{(values) => <>
    <SelectField label="云厂商" name="provider" initialValue={values.get('provider')}
      options={[['', '全部厂商'], ['tencent-cvm', '腾讯云'], ['alibaba-ecs', '阿里云'], ['huawei-ecs', '华为云']]} />
    <label>账号<input name="account" maxLength={128} defaultValue={values.get('account') ?? ''} /></label>
    <label>地域<input name="region" maxLength={128} defaultValue={values.get('region') ?? ''} /></label>
    <SelectField label="关注项" name="finding" initialValue={values.get('finding')} options={findings} />
    <label>观测有效期（秒）<input name="window_seconds" type="number" min={60} max={604800} required
      defaultValue={values.get('window_seconds') ?? 86400} /></label>
    <label>证据最大年龄（秒）<input name="max_age_seconds" type="number" min={60} max={2592000} required
      defaultValue={values.get('max_age_seconds') ?? 86400} /></label>
  </>}</CollectionFilters>
}
