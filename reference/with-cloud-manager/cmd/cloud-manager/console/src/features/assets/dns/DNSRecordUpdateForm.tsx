import { OperationForm } from '../../operations/forms/OperationForm'
import { dnsUpdateInputSchema } from './schema'
import type { DNSAsset, DNSRecord } from './types'

type Props = { base: string; asset: DNSAsset; record: DNSRecord; observedAt: string; stamp: number }

export function DNSRecordUpdateForm({ base, asset, record, observedAt, stamp }: Props) {
  const multiple = asset.provider === 'huawei-ecs'
  const schema = dnsUpdateInputSchema(record.type, multiple ? 128 : 1)
  const existing = record.values ?? []
  return <OperationForm stamp={stamp} target={base + '/dns-zones/record-update'} revision={asset.ownership_revision}
    submitLabel="提交解析修改申请"
    payload={(form) => {
      const input = schema.safeParse({
        account: asset.account, asset_id: asset.id, record_id: record.id, observed_at: observedAt,
        values: String(form.get('values') ?? '').split(/\r?\n/).map((value) => value.trim()).filter(Boolean),
        ttl: Number(form.get('ttl')),
      })
      if (!input.success) throw new Error('请核对记录类型对应的 IP 地址和 TTL。')
      return input.data
    }}>
    <p>{record.name} · {record.type} · 需独立审批</p>
    <p>当前地址 {existing.join('、')} · TTL {record.ttl} 秒</p>
    {multiple
      ? <label>目标 IP 地址（每行一个）<textarea name="values" rows={3} defaultValue={existing.join('\n')} required /></label>
      : <label>目标 IP 地址<input name="values" defaultValue={existing[0]} maxLength={45} required /></label>}
    <label>目标 TTL（秒）<input name="ttl" type="number" min={1} max={604800} defaultValue={record.ttl} required /></label>
  </OperationForm>
}
