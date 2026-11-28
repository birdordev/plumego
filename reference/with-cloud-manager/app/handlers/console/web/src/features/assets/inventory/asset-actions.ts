import { nativeAssetActions } from '../instances/console-actions'
import { bareMetalActions } from '../bare-metal/row-actions'
import { bucketActions } from '../buckets/row-actions'
import { cacheActions } from '../caches/row-actions'
import { clusterActions } from '../clusters/row-actions'
import { databaseActions } from '../databases/row-actions'
import { dnsActions } from '../dns/row-actions'
import { elasticIPActions } from '../elastic-ips/row-actions'
import { imageCreationActions, imageActions } from '../images/row-actions'
import { instanceResizeActions } from '../instances/resize'
import { instanceRetirementActions } from '../instances/retirement'
import { loadBalancerActions } from '../load-balancers/row-actions'
import { networkActions } from '../network/row-actions'
import type { AssetActionContext, ListAssetActions } from '../row-action'
import { snapshotCreationActions, snapshotActions } from '../snapshots/row-actions'
import type { Asset } from '../types'
import { volumeCreationActions, volumeReleaseActions, volumeResizeActions } from '../volumes/row-actions'

const actions: Partial<Record<string, ListAssetActions>> = {
  'kubernetes-cluster': clusterActions,
  'dns-zone': dnsActions,
  'object-bucket': bucketActions,
  'database-instance': databaseActions,
  'cache-instance': cacheActions,
  'bare-metal': bareMetalActions,
  'elastic-ip': elasticIPActions,
  'load-balancer': loadBalancerActions,
  zone: volumeCreationActions,
  volume: (asset, context) => [...snapshotCreationActions(asset, context), ...volumeResizeActions(asset, context), ...volumeReleaseActions(asset, context)],
  snapshot: snapshotActions,
  image: imageActions,
  instance: (asset, context) => [
    ...nativeAssetActions(asset, context), ...imageCreationActions(asset, context),
    ...instanceResizeActions(asset, context), ...instanceRetirementActions(asset, context),
  ],
  network: networkActions,
  subnet: networkActions,
  'security-group': networkActions,
  'security-group-rule': networkActions,
}

export function assetActions(asset: Asset, context: AssetActionContext) {
  const factory = Object.hasOwn(actions, asset.kind) ? actions[asset.kind] : undefined
  return factory?.(asset, context) ?? []
}
