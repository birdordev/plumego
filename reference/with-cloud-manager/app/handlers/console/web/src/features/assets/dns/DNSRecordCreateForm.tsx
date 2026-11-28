import { OperationForm } from '../../operations/forms/OperationForm'
import { dnsCreationInputSchema } from './schema'
import type { DNSAsset } from './types'

type Props = { base: string; asset: DNSAsset; observedAt: string; stamp: number }

export function DNSRecordCreateForm({ base, asset, observedAt, stamp }: Props) {
  return <>
    <p>{asset.account} · {asset.name} · 需独立审批</p>
    <OperationForm stamp={stamp} target={base + '/dns-zones/records'} revision={asset.ownership_revision} submitLabel="提交创建申请"
      payload={(form) => {
        const input = dnsCreationInputSchema.safeParse({
          account: asset.account, asset_id: asset.id, observed_at: observedAt,
          name: form.get('name'), type: form.get('type'), value: String(form.get('value') ?? '').trim(),
          ttl: Number(form.get('ttl')),
        })
        if (!input.success) throw new Error('请核对完整记录名称、地址类型和 TTL。')
        return input.data
      }}>
      <label>完整名称<input name="name" required maxLength={254} placeholder={'www.' + asset.name} /></label>
      <label>类型<select name="type"><option value="A">A</option><option value="AAAA">AAAA</option></select></label>
      <label>IP 地址<input name="value" required maxLength={45} /></label>
      <label>TTL（秒）<input name="ttl" type="number" min={1} max={604800} defaultValue={600} required /></label>
    </OperationForm>
  </>
}
