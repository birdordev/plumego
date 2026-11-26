import type { ListAssetActions } from '../row-action'
import { canReleaseElasticIP, canUnbindElasticIP, showElasticIPRelease } from './actions'
import { showElasticIPBinding } from './binding'

export const elasticIPActions: ListAssetActions = (asset, { canExecute }) => {
  if (!canExecute) return []
  if (canReleaseElasticIP(asset)) return [
    { label: '申请绑定', run: () => showElasticIPBinding(asset, true) },
    { label: '申请释放', run: () => showElasticIPRelease(asset) },
  ]
  return canUnbindElasticIP(asset) ? [{ label: '申请解绑', run: () => showElasticIPBinding(asset, false) }] : []
}
