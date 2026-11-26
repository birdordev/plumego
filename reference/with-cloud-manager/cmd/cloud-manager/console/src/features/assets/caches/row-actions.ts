import type { ListAssetActions } from '../row-action'
import { showCacheRename } from './rename'

const productsByProvider = new Map([
  ['tencent-cvm', 'tencent-redis'],
  ['alibaba-ecs', 'alibaba-tair'],
  ['huawei-ecs', 'huawei-dcs'],
])

export const cacheActions: ListAssetActions = (asset, { canExecute }) => {
  const cache = asset.cache_instance
  if (!canExecute || asset.inventory_state !== 'observed' || asset.ownership_revision <= 0 ||
    cache?.engine !== 'redis' || productsByProvider.get(asset.provider) !== cache.product) return []
  return [{ label: '申请改名', run: () => showCacheRename(asset) }]
}
