import type { z } from 'zod'
import type { createRequester } from '../api/requests'

export type PageRequest = {
  base: string
  admin: boolean
  filters: string
  after: string
  signal: AbortSignal
}

export function collectionReader<Page extends { items: unknown[]; next?: string }>(
  request: ReturnType<typeof createRequester>,
  path: string,
  schema: z.ZodType<Page>,
  cursor: 'cursor' | 'after',
  configureFilters?: (query: URLSearchParams, admin: boolean) => void,
) {
  return ({ base, admin, filters, after, signal }: PageRequest) => {
    const query = new URLSearchParams(filters)
    query.set('limit', '30')
    configureFilters?.(query, admin)
    if (after) query.set(cursor, after)
    return request(base + path + '?' + query, { signal }, schema)
  }
}
