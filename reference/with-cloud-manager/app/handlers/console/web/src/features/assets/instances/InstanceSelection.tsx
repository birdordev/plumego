import { useSyncExternalStore } from 'react'
import { setWorkspaceNotice } from '../../../shared/feedback/notice'
import { assetKey, type Asset } from '../types'
import { assetSelected, setSelectedAsset, subscribeSelection } from './selection-store'

export function InstanceSelection({ asset }: { asset: Asset }) {
  const key = assetKey(asset)
  const checked = useSyncExternalStore(subscribeSelection, () => assetSelected(key))

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
