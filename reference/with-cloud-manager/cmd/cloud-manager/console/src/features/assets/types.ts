import type { z } from 'zod'
import type { assetSchema } from './schema'

export type Asset = z.infer<typeof assetSchema>

export function assetKey(asset: Asset) {
  return [asset.account, asset.region, asset.kind, asset.id].join('\0')
}
