import type { z } from 'zod'
import type { objectBucketSchema } from './schema'

const productNames: Record<string, string> = {
  'tencent-cos': 'COS',
  'alibaba-oss': 'OSS',
  'huawei-obs': 'OBS',
}

export function BucketSummary({ bucket }: { bucket: z.infer<typeof objectBucketSchema> }) {
  return <span className="minor">
    {[productNames[bucket.product] ?? bucket.product, bucket.location, bucket.storage_class].filter(Boolean).join(' · ')}
  </span>
}
