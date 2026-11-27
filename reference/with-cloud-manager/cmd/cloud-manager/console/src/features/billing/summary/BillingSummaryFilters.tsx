import { CollectionFilters } from '../../../shared/collection/CollectionFilters'
import { SelectField } from '../../../shared/components/SelectField'

const providers = [
  ['', '全部厂商'], ['tencent-cvm', '腾讯云'], ['alibaba-ecs', '阿里云'], ['huawei-ecs', '华为云'],
] as const

export function BillingSummaryFilters({ filters, apply }: { filters: string; apply: (filters: string) => void }) {
  return <CollectionFilters values={filters} onApply={apply}>{(values) => <>
    <label>起始账期<input type="month" name="from" required min="2000-01" defaultValue={values.get('from') ?? ''} /></label>
    <label>最后账期<input type="month" name="through" required min="2000-01" defaultValue={values.get('through') ?? ''} /></label>
    <SelectField label="账单来源" name="source" options={[[ 'native', '厂商账单' ], ['focus-1.0', 'FOCUS 1.0']]}
      initialValue={values.get('source')} />
    <label>原币种<input name="currency" required pattern="[A-Z]{3}" maxLength={3} defaultValue={values.get('currency') ?? 'CNY'} /></label>
    <SelectField label="云厂商" name="provider" initialValue={values.get('provider')} options={providers} />
    <SelectField label="账号明细" name="missing_only" options={[[ '', '全部账号' ], ['true', '仅待导入']]}
      initialValue={values.get('missing_only')} />
  </>}</CollectionFilters>
}
