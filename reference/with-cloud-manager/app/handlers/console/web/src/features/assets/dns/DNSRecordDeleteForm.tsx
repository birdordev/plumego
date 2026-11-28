import { OperationForm } from '../../operations/forms/OperationForm'
import { dnsDeletionInputSchema } from './schema'
import type { DNSAsset, DNSRecord } from './types'

type Props = { asset: DNSAsset; base: string; record: DNSRecord; observedAt: string; stamp: number }

export function DNSRecordDeleteForm({ asset, base, record, observedAt, stamp }: Props) {
  return <>
    <p>{[asset.account, asset.name, record.name, record.type, record.line, record.values?.join('、')].filter(Boolean).join(' · ')}</p>
    <p>记录 ID {record.id} · 已停用 · 缓存时间 {record.ttl} 秒</p>
    <p>独立审批通过后删除此记录。删除后如需恢复，应重新创建记录。</p>
    <OperationForm stamp={stamp} target={base + '/dns-zones/record-deletion'} revision={asset.ownership_revision}
      submitLabel="提交删除申请" payload={() => {
        const data = dnsDeletionInputSchema.safeParse({
          account: asset.account, asset_id: asset.id, record_id: record.id, observed_at: observedAt,
        })
        if (!data.success || record.status !== 'disable') throw new Error('当前记录已变化，请刷新快照。')
        return data.data
      }} />
  </>
}
