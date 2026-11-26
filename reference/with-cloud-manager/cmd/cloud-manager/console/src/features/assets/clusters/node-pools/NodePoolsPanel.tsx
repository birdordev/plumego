import { ConsoleTable } from '../../../../shared/components/ConsoleTable'
import { ClusterSnapshotPanel } from '../ClusterSnapshotPanel'
import type { ClusterAsset } from '../snapshot'
import { readNodePools } from './api'
import type { NodePool } from './schema'

const columns = ['节点池', '类型', '状态', '当前节点', '期望节点', '伸缩范围', '自动伸缩']

function nodePoolRow(pool: NodePool) {
  return {
    key: pool.id,
    cells: [
      <>{pool.name}<span className="minor">{pool.id}</span></>,
      pool.type || '—', pool.status || '—', pool.current_nodes, pool.desired_nodes,
      pool.min_nodes === undefined && pool.max_nodes === undefined
        ? '—' : (pool.min_nodes ?? '—') + ' ～ ' + (pool.max_nodes ?? '—'),
      pool.autoscaling === undefined ? '—' : pool.autoscaling ? '已开启' : '已关闭',
    ],
  }
}

export default function NodePoolsPanel({ base, cluster, signal }: { base: string; signal: AbortSignal; cluster: ClusterAsset }) {
  return <ClusterSnapshotPanel base={base} signal={signal} cluster={cluster} label="节点池" unit="个" read={readNodePools}>
    {(items) => <ConsoleTable columns={columns} rows={items.map(nodePoolRow)} />}
  </ClusterSnapshotPanel>
}
