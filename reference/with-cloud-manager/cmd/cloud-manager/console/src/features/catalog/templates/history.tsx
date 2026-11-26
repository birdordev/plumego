import { useState } from 'react'
import { DetailFields, type DetailField } from '../../../shared/components/DetailFields'
import { DetailForm } from '../../../shared/detail/DetailForm'
import { formText } from '../../../shared/detail/form-values'
import { openDetail } from '../../../shared/detail/store'
import { catalogFields } from './labels'
import type { CatalogTemplate } from '../schema'
import { readTemplateVersion } from './api'

export function showCatalogHistory(item: CatalogTemplate, base: string) {
  openDetail('模板历史版本', (stamp, signal) => <HistoryForm item={item} base={base} stamp={stamp} signal={signal} />)
}

function HistoryForm({ item, base, stamp, signal }:
  { item: CatalogTemplate; base: string; stamp: number; signal: AbortSignal }) {
  const [fields, setFields] = useState<DetailField[]>([])
  return <>
    <DetailForm stamp={stamp} submitLabel="查看版本" onSubmit={(data) => {
      setFields([])
      return readTemplateVersion(base, formText(data, 'version'), signal)
    }} onSaved={(saved) => setFields([
      ['发布人', saved.published_by],
      ...Object.entries(catalogFields).map(([field, label]): DetailField => [label, saved[field as keyof typeof catalogFields]]),
    ])}>
      <label>版本号<input name="version" defaultValue={item.version} required maxLength={19} pattern="[1-9][0-9]*" inputMode="numeric" /></label>
    </DetailForm>
    {fields.length ? <DetailFields fields={fields} /> : null}
  </>
}
