import type { DetailField } from '../../../../shared/components/DetailFields'
import type { OperationDetail } from '../schema'

export function workloadScaleFields(op: OperationDetail): DetailField[] {
  const intent = op.workload_scale_intent
  if (!intent) return []
  return [
    ['工作负载', intent.kind + ' / ' + intent.namespace + ' / ' + intent.name],
    ['工作负载 UID', intent.uid],
    ['原期望副本', intent.current_replicas],
    ['目标副本', intent.replicas],
  ]
}
