import { lazy, Suspense } from 'react'
import { openDetail } from '../../../shared/detail/store'
import { projectURL } from '../../session/project'
import type { Asset } from '../types'

const ConfigurationPanel = lazy(() => import('./ConfigurationPanel'))

export function canViewLoadBalancerConfiguration(asset: Asset) {
  return asset.kind === 'load-balancer' && asset.load_balancer && asset.inventory_state === 'observed'
}

export function showLoadBalancerConfiguration(asset: Asset) {
  const query = new URLSearchParams({ account: asset.account, region: asset.region })
  const target = projectURL() + '/assets/load-balancers/' + encodeURIComponent(asset.id) + '/configuration?' + query
  openDetail('负载均衡配置', (_stamp, signal) => (
    <Suspense fallback={<p role="status">正在读取最近一次完整快照…</p>}>
      <ConfigurationPanel target={target} asset={asset} signal={signal} />
    </Suspense>
  ))
}
