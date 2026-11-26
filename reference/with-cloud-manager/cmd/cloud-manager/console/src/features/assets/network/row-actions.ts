import type { ListAssetActions } from '../row-action'
import { canDeleteNetwork, canRevokeRule, networkDeletionLabels, showNetworkDeletion, showSecurityRuleRevocation } from './actions'

export const networkActions: ListAssetActions = (asset, { canExecute }) => {
  if (!canExecute) return []
  const label = canDeleteNetwork(asset) && networkDeletionLabels[asset.kind]
  if (label) return [{ label, run: () => showNetworkDeletion(asset) }]
  return canRevokeRule(asset) ? [{ label: '申请撤销', run: () => showSecurityRuleRevocation(asset) }] : []
}
