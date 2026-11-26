import { z } from 'zod'

export function pageSchema<T extends z.ZodType>(item: T) {
  return z.object({ items: z.array(item).max(200), next: z.string().optional() })
}
