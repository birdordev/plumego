import { OperationForm } from '../../operations/forms/OperationForm'
import { dnsStatusInputSchema } from './schema'
import type { DNSAsset, DNSRecord } from './types'

type Props = { asset: DNSAsset; base: string; record: DNSRecord; observedAt: string; enabled: boolean; stamp: number }

export function DNSRecordStatusForm({ asset, base, record, observedAt, enabled, stamp }: Props) {
  return <>
    <p>{[asset.account, asset.name, record.name, record.type, record.values?.join('、')].filter(Boolean).join(' · ')}</p>
    <p>当前状态 {record.status} · 缓存时间 {record.ttl} 秒 · 需独立审批</p>
    <OperationForm stamp={stamp} target={base + '/dns-zones/record-status'} revision={asset.ownership_revision}
      submitLabel="提交申请" payload={() => {
        const data = dnsStatusInputSchema.safeParse({
          account: asset.account, asset_id: asset.id, record_id: record.id, observed_at: observedAt, enabled,
        })
        if (!data.success) throw new Error('当前记录已变化，请刷新快照。')
        return data.data
      }} />
  </>
}
