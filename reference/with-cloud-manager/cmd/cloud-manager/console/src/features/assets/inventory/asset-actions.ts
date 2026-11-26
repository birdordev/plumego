import { nativeAssetActions } from '../instances/console-actions'
import { cacheActions } from '../caches/row-actions'
import { clusterActions } from '../clusters/row-actions'
import { databaseActions } from '../databases/row-actions'
import { dnsActions } from '../dns/row-actions'
import { elasticIPActions } from '../elastic-ips/row-actions'
import { imageCreationActions, imageActions } from '../images/row-actions'
import { instanceRetirementActions } from '../instances/retirement'
import { loadBalancerActions } from '../load-balancers/row-actions'
import { networkActions } from '../network/row-actions'
import type { AssetActionContext, ListAssetActions } from '../row-action'
import { snapshotCreationActions, snapshotActions } from '../snapshots/row-actions'
import type { Asset } from '../types'

const actions: Partial<Record<string, ListAssetActions>> = {
  'kubernetes-cluster': clusterActions,
  'dns-zone': dnsActions,
  'database-instance': databaseActions,
  'cache-instance': cacheActions,
  'elastic-ip': elasticIPActions,
  'load-balancer': loadBalancerActions,
  volume: snapshotCreationActions,
  snapshot: snapshotActions,
  image: imageActions,
  instance: (asset, context) => [
    ...nativeAssetActions(asset, context), ...imageCreationActions(asset, context), ...instanceRetirementActions(asset, context),
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
