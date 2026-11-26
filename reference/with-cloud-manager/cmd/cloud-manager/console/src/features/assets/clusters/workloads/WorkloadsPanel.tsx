import { ConsoleTable } from '../../../../shared/components/ConsoleTable'
import { ClusterSnapshotPanel } from '../ClusterSnapshotPanel'
import type { WorkloadCluster } from './view'
import { showWorkloadScale } from './ScaleForm'
import { readWorkloads } from './api'
import type { Workload } from './schema'

const columns = ['名称', '命名空间', '类型', '期望数量', '就绪', '活动', '成功', '失败', '暂停']

function workloadRow(item: Workload, action?: { enabled: boolean; run: () => void }) {
  return {
    key: item.uid,
    cells: [item.name, item.namespace, item.kind, item.desired, item.ready, item.active,
      item.succeeded, item.failed, item.suspended === undefined ? '—' : item.suspended ? '是' : '否',
      ...(action ? [item.desired !== undefined && ['Deployment', 'StatefulSet'].includes(item.kind)
        ? <button type="button" disabled={!action.enabled} onClick={action.run}>调整副本</button> : '—'] : [])],
  }
}

export default function WorkloadsPanel({ base, cluster, signal, canExecute }: { base: string; signal: AbortSignal; cluster: WorkloadCluster; canExecute: boolean }) {
  const canScale = canExecute && cluster.ownership_revision > 0
  return <ClusterSnapshotPanel base={base} signal={signal} cluster={cluster} label="工作负载" unit="项" read={readWorkloads}>
    {(items, snapshot, usable) => <ConsoleTable columns={canScale ? [...columns, '操作'] : columns}
      rows={items.map((item) => workloadRow(item, canScale
        ? { enabled: usable, run: () => showWorkloadScale(base, cluster, snapshot, item) } : undefined))} />}
  </ClusterSnapshotPanel>
}
