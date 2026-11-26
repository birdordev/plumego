import type { z } from 'zod'
import type { clusterSchema } from './schema'

const products: Record<string, string> = {
  'tencent-tke': 'TKE',
  'alibaba-ack': 'ACK',
  'huawei-cce': 'CCE',
}

export function ClusterSummary({ cluster }: { cluster: z.infer<typeof clusterSchema> }) {
  const fields = [products[cluster.product] ?? cluster.product, cluster.cluster_type, cluster.version]
  if (cluster.node_count !== undefined) fields.push(cluster.node_count + ' 个节点')
  return <span className="minor">{fields.filter(Boolean).join(' · ')}</span>
}
