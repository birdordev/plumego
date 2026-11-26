import { canExecute } from '../../session/project'
import { openDetail } from '../../../shared/detail/store'
import { timestamp } from '../../../shared/display/labels'
import { DNSRecordCreateForm } from './DNSRecordCreateForm'
import { DNSRecordDeleteForm } from './DNSRecordDeleteForm'
import { DNSRecordStatusForm } from './DNSRecordStatusForm'
import type { DNSAsset } from './types'
import { useDNSRecords } from './useDNSRecords'

export default function DNSRecordsPanel({ base, asset, signal }: { base: string; asset: DNSAsset; signal: AbortSignal }) {
  const { snapshot, changed, error, loading, loadNext, reload } = useDNSRecords(base, asset, signal)
  const mayApply = asset.ownership_revision > 0 && canExecute()
  return (
    <>
      {snapshot.observedAt ? <p>{asset.name} · {snapshot.total} 条 · 采集时间 {timestamp(snapshot.observedAt)}</p> : null}
      {loading ? <p role="status">正在读取 DNS 快照…</p> : null}
      {error ? <p role="alert" className="notice" data-error="true">{error}</p> : null}
      {changed ? <p role="status">快照已更新，请重新加载。</p> : null}
      {snapshot.observedAt && !snapshot.total ? <p>当前快照没有解析记录。</p> : null}
      {mayApply && snapshot.observedAt && !changed && !error ? (
        <button type="button" onClick={() => openDetail('申请添加公网解析记录', (stamp) => (
          <DNSRecordCreateForm asset={asset} base={base} observedAt={snapshot.observedAt} stamp={stamp} />
        ))}>申请添加记录</button>
      ) : null}
      <ul>
        {snapshot.records.map((record) => {
          const values = record.values?.join('、') || (record.value_count ? record.value_count + ' 个值（已隐藏）' : '')
          const enabled = record.status === 'active' || record.status === 'enable'
          const editable = mayApply && !changed && !error && (enabled || record.status === 'disable') &&
            (record.type === 'A' || record.type === 'AAAA') && Boolean(record.values?.length)
          return (
            <li key={record.id}>
              {[record.name, record.type, 'TTL ' + record.ttl, record.status, record.line, values].filter(Boolean).join(' · ')}
              {' '}
              {editable ? <button type="button" onClick={() => openDetail(
                enabled ? '申请停用公网解析' : '申请启用公网解析',
                (stamp) => <DNSRecordStatusForm asset={asset} base={base} record={record} observedAt={snapshot.observedAt}
                  enabled={!enabled} stamp={stamp} />,
              )}>{enabled ? '申请停用解析' : '申请启用解析'}</button> : null}
              {editable && record.status === 'disable' ? <button type="button" onClick={() => openDetail(
                '申请删除公网解析记录',
                (stamp) => <DNSRecordDeleteForm asset={asset} base={base} record={record} observedAt={snapshot.observedAt}
                  stamp={stamp} />,
              )}>申请删除解析</button> : null}
            </li>
          )
        })}
      </ul>
      {changed ? <button type="button" disabled={loading} onClick={reload}>重新加载快照</button> : null}
      {!changed && snapshot.next ? <button type="button" disabled={loading} onClick={loadNext}>加载更多</button> : null}
    </>
  )
}
