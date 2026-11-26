import { useSyncExternalStore } from 'react'
import { ConsoleTable } from '../../shared/components/ConsoleTable'
import { inventoryStates, kinds } from '../../shared/display/labels'
import { setWorkspaceNotice } from '../../shared/feedback/notice'
import { assetSelected, setSelectedAsset, subscribeSelection } from './selection-store'
import { AssetActions } from './AssetActions'
import { assetActions } from './asset-actions'
import type { AssetActionContext } from './row-action'
import { BucketSummary } from './buckets/BucketSummary'
import { CacheSummary } from './caches/CacheSummary'
import { ClusterSummary } from './clusters/ClusterSummary'
import { DatabaseSummary } from './databases/DatabaseSummary'
import { assetKey, type Asset } from './types'

function InstanceSelection({ asset }: { asset: Asset }) {
  const key = assetKey(asset)
  const checked = useSyncExternalStore(subscribeSelection, () => assetSelected(key))
  if (asset.kind !== 'instance') return '—'
  return <input type="checkbox" aria-label={'选择 ' + (asset.name || asset.id)}
    checked={checked} onChange={(event) => {
      const input = event.currentTarget
      if (!setSelectedAsset(key, asset, input.checked)) {
        input.checked = false
        setWorkspaceNotice('每批最多选择 25 个实例。', true)
      }
    }}
  />
}

function AssetName({ asset }: { asset: Asset }) {
  const lb = asset.load_balancer
  const snapshot = [asset.parent_id && '源卷 ' + asset.parent_id, asset.snapshot?.class,
    asset.snapshot?.size_gib && asset.snapshot.size_gib + ' GiB'].filter(Boolean).join(' · ')
  return <div>
    {asset.name || asset.id}
    {asset.name && asset.name !== asset.id ? <span className="minor">{asset.id}</span> : null}
    {asset.public_addresses?.length ? <span className="minor">{asset.public_addresses.join(' · ')}</span> : null}
    {lb?.product ? <span className="minor">{lb.product}</span> : null}
    {lb?.dns_name ? <span className="minor">{lb.dns_name}</span> : null}
    {lb?.private_addresses?.length ? <span className="minor">私网 {lb.private_addresses.join(' · ')}</span> : null}
    {asset.object_bucket ? <BucketSummary bucket={asset.object_bucket} /> : null}
    {asset.kubernetes_cluster ? <ClusterSummary cluster={asset.kubernetes_cluster} /> : null}
    {asset.cache_instance ? <CacheSummary cache={asset.cache_instance} /> : null}
    {asset.database_instance ? <DatabaseSummary database={asset.database_instance} /> : null}
    {asset.kind === 'snapshot' && snapshot ? <span className="minor">{snapshot}</span> : null}
  </div>
}

export function AssetsTable({ items, editable, ...access }: { items: readonly Asset[]; editable: boolean } & Omit<AssetActionContext, 'canExecute'>) {
  const actionsByRow = items.map((item) => assetActions(item, { ...access, canExecute: editable }))
  const withActions = actionsByRow.some((actions) => actions.length > 0)
  const columns = [...(editable ? ['选择'] : []), '资源', '类型', '账号 / 地域', '状态', '归属版本',
    ...(withActions ? ['操作'] : [])]
  const rows = items.map((item, index) => ({
    key: assetKey(item),
    cells: [
      ...(editable ? [<InstanceSelection asset={item} />] : []),
      <AssetName asset={item} />,
      kinds[item.kind as keyof typeof kinds] || item.kind,
      item.region ? item.account + ' / ' + item.region : item.account,
      inventoryStates[item.inventory_state as keyof typeof inventoryStates] || item.status || item.inventory_state,
      item.ownership_revision,
      ...(withActions ? [<AssetActions actions={actionsByRow[index]} />] : []),
    ],
  }))
  return <ConsoleTable columns={columns} rows={rows} />
}
