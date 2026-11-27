import { z } from 'zod'
import { account, assetId, revision } from '../../../shared/api/fields'

export const batchInputSchema = z.looseObject({
  items: z.array(z.looseObject({
    asset_id: assetId, account, region: z.string().min(1), action: z.string().min(1),
    ownership_revision: revision,
  })).min(1).max(25),
})
