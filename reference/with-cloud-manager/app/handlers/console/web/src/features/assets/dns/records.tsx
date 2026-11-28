import { lazy, Suspense } from 'react'
import { projectURL } from '../../session/project'
import { openDetail } from '../../../shared/detail/store'
import type { DNSAsset } from './types'

const DNSRecordsPanel = lazy(() => import('./DNSRecordsPanel'))

export function canViewDNSRecords(asset: { kind: string; inventory_state: string }) {
  return asset.kind === 'dns-zone' && asset.inventory_state === 'observed'
}

export function showDNSRecords(asset: DNSAsset) {
  const base = projectURL()
  openDetail('公网 DNS 解析记录', (_stamp, signal) => (
    <Suspense fallback={<p role="status">正在加载 DNS 记录…</p>}>
      <DNSRecordsPanel base={base} signal={signal} asset={asset} />
    </Suspense>
  ))
}
