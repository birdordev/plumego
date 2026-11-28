import type { Asset } from '../types'
import type { ListAssetActions } from '../row-action'
import { showVolumeCreation, supportsVolumeCreation } from './create'
import { maxVolumeResizeGiB, showVolumeResize } from './resize'
import { showVolumeRelease } from './release'

function hasObservedDisk(asset: Asset) {
  return asset.inventory_state === 'observed' && asset.ownership_revision > 0 && asset.volume?.size_gib !== undefined
}

const detachedStates = new Map([['tencent-cvm', 'UNATTACHED'], ['alibaba-ecs', 'Available'], ['huawei-ecs', 'available']])

function isPublicDataDisk(asset: Asset) {
  return detachedStates.has(asset.provider) &&
    (asset.provider === 'huawei-ecs' ? asset.volume?.bootable === false : asset.volume?.usage === 'data')
}

export const volumeResizeActions: ListAssetActions = (asset, { canExecute }) => {
  const current = asset.volume?.size_gib
  return canExecute && hasObservedDisk(asset) && isPublicDataDisk(asset) &&
    current !== undefined && current < maxVolumeResizeGiB && detachedStates.get(asset.provider) === asset.status
    ? [{ label: '申请扩容', run: () => showVolumeResize(asset, current) }] : []
}

export const volumeReleaseActions: ListAssetActions = (asset, { canExecute }) =>
  canExecute && hasObservedDisk(asset) && isPublicDataDisk(asset)
    ? [{ label: '释放预览', run: () => showVolumeRelease(asset) }] : []

export const volumeCreationActions: ListAssetActions = (zone, { canExecute }) =>
  canExecute && supportsVolumeCreation(zone) ? [{ label: '申请云盘', run: () => showVolumeCreation(zone) }] : []
