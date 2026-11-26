import { z } from 'zod'
import { openDetail } from '../../../../shared/detail/store'
import { OperationForm } from '../../../operations/forms/OperationForm'
import type { Workload } from './schema'
import type { WorkloadCluster } from './view'

const replicaCount = z.number().int().min(1).max(100)
type Target = { base: string; cluster: WorkloadCluster; snapshot: string; item: Workload }

export function showWorkloadScale(base: string, cluster: WorkloadCluster, snapshot: string, item: Workload) {
  openDetail('调整工作负载副本', (stamp) =>
    <ScaleForm base={base} cluster={cluster} snapshot={snapshot} item={item} stamp={stamp} />)
}

function ScaleForm({ base, cluster, snapshot, item, stamp }: Target & { stamp: number }) {
  return <>
    <p>{item.namespace} / {item.name} · 当前期望副本 {item.desired}</p>
    <p className="hint">提交后需独立审批，实际可用副本由集群调度决定。</p>
    <OperationForm stamp={stamp} target={base + '/kubernetes-workloads/scale'} revision={cluster.ownership_revision}
      submitLabel="提交副本调整申请" payload={(data) => {
        const desired = replicaCount.safeParse(Number(data.get('replicas')))
        if (!desired.success || desired.data === item.desired) throw new Error('请输入与当前值不同的整数，范围为 1 至 100。')
        return { account: cluster.account, region: cluster.region, asset_id: cluster.id, snapshot,
          uid: item.uid, namespace: item.namespace, kind: item.kind, name: item.name, replicas: desired.data }
      }}>
      <label>目标副本数<input name="replicas" type="number" min={1} max={100} step={1} required /></label>
    </OperationForm>
  </>
}
