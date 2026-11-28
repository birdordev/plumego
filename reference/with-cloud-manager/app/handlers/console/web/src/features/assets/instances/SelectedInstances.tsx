import { useSyncExternalStore } from 'react'
import { selectionCount, subscribeSelection } from './selection-store'

export function SelectedInstances({ onBatch }: { onBatch: () => void }) {
  const count = useSyncExternalStore(subscribeSelection, selectionCount)
  return count ? <button id="selected" onClick={onBatch} type="button">批量操作（{count}）</button> : null
}
