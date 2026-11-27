import { z } from 'zod'

export const operationReceiptSchema = z.object({ id: z.string().min(1) })
