import { z } from 'zod'
import { time } from '../../shared/api/fields'

export const grantSchema = z.object({
  id: z.string(),
  asset_id: z.string(),
  purpose: z.string(),
  requester: z.string(),
  status: z.string(),
  expires_at: time,
})

export type Grant = z.infer<typeof grantSchema>
