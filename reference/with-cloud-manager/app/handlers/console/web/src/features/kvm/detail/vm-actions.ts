import type { Project } from '../../session/store'
import type { VMAction } from '../api'

export function availableActions(role: Project['role'], state: string): readonly VMAction[] {
  if (state === 'running' && (role === 'operator' || role === 'admin')) return ['shutdown']
  if (state === 'stopped' && role === 'admin') return ['retirement']
  if (role !== 'approver') return []
  switch (state) {
    case 'awaiting_approval':
    case 'shutdown_requested':
    case 'retire_requested': return ['approve', 'reject']
    case 'queued':
    case 'shutdown_queued':
    case 'retire_queued': return ['reject']
    default: return []
  }
}

export const actionLabels = {
  shutdown: '申请优雅关机', retirement: '申请退役', approve: '批准申请', reject: '拒绝申请',
}

export function actionDescription(action: VMAction, state: string) {
  if (action === 'shutdown') return '独立审批通过后，宿主机代理将向虚拟机发送 ACPI 关机请求。'
  if (action === 'retirement') return '独立审批通过后，宿主机代理将移除已关机虚拟机的域定义并保留启动卷。'
  if (action === 'approve') return '批准后，宿主机代理可以领取此申请对应的任务。'
  return state.endsWith('_queued') || state === 'queued' ? '拒绝后，代理将无法领取这条已排队的任务。' : '拒绝后，此申请将结束。'
}
