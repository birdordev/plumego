import { CollectionFilters } from '../../../shared/collection/CollectionFilters'
import { SelectField } from '../../../shared/components/SelectField'

const findings = [
  ['', '全部对象桶'],
  ['inventory_unavailable', '资产盘点待确认'],
  ['encryption_unconfigured', '未配置默认加密'],
  ['versioning_inactive', '版本控制未启用'],
  ['encryption_needs_observation', '默认加密待观测'],
  ['versioning_needs_observation', '版本控制待观测'],
] as const

export function BucketProtectionFilters({ filters, apply }: { filters: string; apply: (input: string) => void }) {
  return <CollectionFilters values={filters} onApply={apply}>{(values) => <>
    <SelectField label="云厂商" name="provider" initialValue={values.get('provider')}
      options={[[ '', '全部厂商' ], ['tencent-cvm', '腾讯云 COS'], ['alibaba-ecs', '阿里云 OSS'], ['huawei-ecs', '华为云 OBS']]} />
    <label>账号<input name="account" defaultValue={values.get('account') ?? ''} maxLength={128} /></label>
    <label>实际地域<input name="location" defaultValue={values.get('location') ?? ''} maxLength={63} /></label>
    <SelectField label="关注项" name="finding" initialValue={values.get('finding')} options={findings} />
    <label>有效观测窗口（秒）<input name="window_seconds" type="number" min={60} max={604800}
      defaultValue={values.get('window_seconds') ?? 86400} required /></label>
  </>}</CollectionFilters>
}
