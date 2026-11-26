import { z } from 'zod'
import { time } from '../../shared/api/fields'
import { pageSchema } from '../../shared/api/page'

export const operationSchema = z.object({
  id: z.string(),
  action: z.string(),
  status: z.string(),
  asset_id: z.string(),
  created_at: time,
})

export const operationPageSchema = pageSchema(operationSchema)

export const operationReceiptSchema = z.object({ id: z.string().min(1) })
