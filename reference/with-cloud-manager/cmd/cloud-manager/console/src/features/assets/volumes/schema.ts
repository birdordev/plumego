import { z } from 'zod'

export const volumeSchema = z.object({
  size_gib: z.number().int().positive().optional(),
  storage_class: z.string().optional(),
  usage: z.enum(['system', 'data']).optional(),
  bootable: z.boolean().optional(),
})
