import type { ListAssetActions } from '../row-action'
import { showBareMetalPXE } from './pxe'
import { showBareMetalPower } from './power'
import { showBareMetalReclaim } from './reclaim/view'

export const bareMetalActions: ListAssetActions = (asset, { canExecute }) => {
  if (asset.provider !== 'redfish' || asset.inventory_state !== 'observed' ||
    asset.ownership_revision <= 0) return []
  const reclaim = { label: '回收与归属', run: () => showBareMetalReclaim(asset) }
  if (!canExecute) return [reclaim]
  if (asset.status === 'off') return [
    reclaim,
    { label: '申请开机', run: () => showBareMetalPower(asset, 'power-on-bare-metal') },
    { label: '配置一次性 PXE', run: () => showBareMetalPXE(asset, 'stage-bare-metal-pxe') },
    { label: '取消一次性 PXE', run: () => showBareMetalPXE(asset, 'cancel-bare-metal-pxe') },
  ]
  if (asset.status === 'on') return [reclaim, { label: '申请优雅关机', run: () => showBareMetalPower(asset, 'shutdown-bare-metal') }]
  return [reclaim]
}
