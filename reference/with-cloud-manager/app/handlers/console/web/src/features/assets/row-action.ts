import type { Asset } from './types'

export type AssetRowAction = { label: string; run: () => void }
export type AssetActionContext = { canExecute: boolean; nativeConsole: boolean; nativeSerial: boolean }
export type ListAssetActions = (asset: Asset, context: AssetActionContext) => AssetRowAction[]
