import { lazy, Suspense } from 'react'
import { openDetail } from '../../../shared/detail/store'
import { projectURL } from '../../session/project'
import type { Asset } from '../types'

const DatabaseRestoreWindowsPanel = lazy(() => import('./DatabaseRestoreWindowsPanel'))

export function showRestoreWindows(asset: Asset, canExecute: boolean) {
  const query = new URLSearchParams({ account: asset.account, region: asset.region })
  const target = projectURL() + '/assets/databases/' + encodeURIComponent(asset.id) + '/restore-windows?' + query
  openDetail('数据库可恢复时间', (_stamp, signal) => (
    <Suspense fallback={<p role="status">正在加载可恢复时间…</p>}>
      <DatabaseRestoreWindowsPanel target={target} asset={asset} canExecute={canExecute} signal={signal} />
    </Suspense>
  ))
}
