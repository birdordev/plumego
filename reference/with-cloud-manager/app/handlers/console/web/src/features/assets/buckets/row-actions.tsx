import { lazy, Suspense } from 'react'
import { openDetail } from '../../../shared/detail/store'
import { projectURL } from '../../session/project'
import type { ListAssetActions } from '../row-action'
import type { Asset } from '../types'

const BucketSettingPanel = lazy(() => import('./BucketSettingPanel'))

export const bucketActions: ListAssetActions = (asset, { canExecute }) =>
  asset.object_bucket && asset.inventory_state === 'observed'
    ? [{ label: '查看版本控制', run: () => showSetting(asset, 'versioning', '对象桶版本控制', canExecute) },
      { label: '查看默认加密', run: () => showSetting(asset, 'encryption', '对象桶默认加密', canExecute) }]
    : []

function showSetting(asset: Asset, setting: 'versioning' | 'encryption', title: string, canExecute: boolean) {
  const query = new URLSearchParams({ account: asset.account })
  const base = projectURL()
  const target = base + '/assets/object-buckets/' + encodeURIComponent(asset.id) + '/' + setting + '?' + query
  const mutation = canExecute && asset.ownership_revision > 0 && !asset.name.includes('.')
    ? { url: base + '/object-buckets/' + setting, account: asset.account, id: asset.id, revision: asset.ownership_revision }
    : undefined
  openDetail(title, (stamp, signal) => (
    <Suspense fallback={<p role="status">正在加载{title}…</p>}>
      <BucketSettingPanel target={target} name={asset.name} signal={signal} setting={setting} mutation={mutation} stamp={stamp} />
    </Suspense>
  ))
}
