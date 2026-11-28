import { lazy, Suspense } from 'react'
import { openDetail } from '../../../shared/detail/store'
import { projectURL } from '../../session/project'
import type { Asset } from '../types'

const DatabaseBackupsPanel = lazy(() => import('./DatabaseBackupsPanel'))

export function showBackups(asset: Asset) {
  const query = new URLSearchParams({ account: asset.account, region: asset.region })
  const target = projectURL() + '/assets/databases/' + encodeURIComponent(asset.id) + '/backups?' + query
  openDetail('数据库备份', (_stamp, signal) => (
    <Suspense fallback={<p role="status">正在加载备份…</p>}>
      <DatabaseBackupsPanel target={target} name={asset.name} signal={signal} />
    </Suspense>
  ))
}
