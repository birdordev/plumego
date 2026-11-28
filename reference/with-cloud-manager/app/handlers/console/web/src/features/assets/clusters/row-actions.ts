import type { ListAssetActions } from '../row-action'
import { showNodePools } from './node-pools/view'
import { showWorkloads } from './workloads/view'

export const clusterActions: ListAssetActions = (asset, context) =>
  asset.kind === 'kubernetes-cluster' && asset.inventory_state === 'observed'
    ? [
        { label: '查看节点池', run: () => showNodePools(asset) },
        { label: '查看工作负载', run: () => showWorkloads(asset, context.canExecute) },
      ]
    : []
