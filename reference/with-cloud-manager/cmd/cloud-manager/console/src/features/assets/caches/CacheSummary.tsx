import type { z } from 'zod'
import type { cacheSchema } from './schema'

const engines = { redis: 'Redis', memcached: 'Memcached', ckv: 'CKV' }

export function CacheSummary({ cache }: { cache: z.infer<typeof cacheSchema> }) {
  const engine = [engines[cache.engine], cache.version].filter(Boolean).join(' ')
  return <span className="minor">
    {cache.capacity_mib ? engine + ' · ' + cache.capacity_mib + ' MiB' : engine}
  </span>
}
