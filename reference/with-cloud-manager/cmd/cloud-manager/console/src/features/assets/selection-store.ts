import type { Asset } from './types'

const selection = new Map<string, Asset>()
const listeners = new Set<() => void>()

function notifySelection() {
  listeners.forEach((notify) => notify())
}

export function subscribeSelection(notify: () => void) {
  listeners.add(notify)
  return () => { listeners.delete(notify) }
}

export const selectionCount = () => selection.size
export const assetSelected = (key: string) => selection.has(key)
export const selectedAssets = () => Array.from(selection.values())

export function setSelectedAsset(key: string, asset: Asset, checked: boolean) {
  if (checked && selection.size >= 25 && !selection.has(key)) return false
  if (checked) selection.set(key, asset)
  else selection.delete(key)
  notifySelection()
  return true
}

export function clearSelection() {
  if (!selection.size) return
  selection.clear()
  notifySelection()
}
