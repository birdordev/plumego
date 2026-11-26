import type { AssetRowAction, ListAssetActions } from '../row-action'
import { showLoadBalancerRename } from './rename'
import { canViewLoadBalancerConfiguration, showLoadBalancerConfiguration } from './view'

export const loadBalancerActions: ListAssetActions = (asset, { canExecute }) => {
  if (!canViewLoadBalancerConfiguration(asset)) return []
  const actions: AssetRowAction[] = [{ label: '查看配置', run: () => showLoadBalancerConfiguration(asset) }]
  if (canExecute && asset.ownership_revision > 0) actions.push({ label: '申请改名', run: () => showLoadBalancerRename(asset) })
  return actions
}
