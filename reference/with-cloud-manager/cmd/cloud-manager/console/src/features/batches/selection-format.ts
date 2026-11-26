import { selectedAssets } from '../assets/instances/selection-store'

export function formatSelectedInstances(action: string) {
  const items = selectedAssets().map((asset) => ({
    account: asset.account, region: asset.region, asset_id: asset.id,
    ownership_revision: asset.ownership_revision, action,
  }))
  return JSON.stringify({ items }, null, 2)
}
