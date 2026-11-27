import type { Project } from '../../session/store'
import type { HostDetail } from '../schema'
import type { HostAction } from './api'
import type { FenceHistory, WorkPage } from './schema'

export const labels = {
  request: '申请隔离', confirm: '确认带外隔离', 'request-rejoin': '申请重新接入',
  'approve-rejoin': '批准重新接入', 'reject-rejoin': '拒绝重新接入', drain: '停止新放置', resume: '恢复新放置',
} satisfies Record<HostAction, string>

export const descriptions = {
  request: '申请后立即推进代理代次，停止新放置、代理上报和任务派发。原有预留和未决任务将保留。',
  confirm: '请先在现场核对宿主机身份，以及原宿主机及其网络已通过带外手段隔离。确认可能激活已登记的疏散预案。',
  'request-rejoin': '请填写现场证据和新的项目代理服务主体。重新接入需要独立审批。',
  'approve-rejoin': '批准会绑定申请中的代理，推进代次并保留排空状态。恢复新放置还需管理员操作及新的容量上报。',
  'reject-rejoin': '宿主机将回到已隔离状态，可以继续处置当前阻塞任务。',
  drain: '停止接收新放置，现有虚拟机和预留仍保留。',
  resume: '恢复接收新放置。新代理提交有效容量报告后才具备放置资格。',
} satisfies Record<HostAction, string>

export function sameFenceSnapshot(host: HostDetail, work: WorkPage) {
  return host.id === work.host && host.uuid === work.host_uuid && host.revision === work.host_revision &&
    host.generation === work.host_generation && (host.fence_state ?? '') === work.fence_state
}

export function availableFenceActions({ host, events }: FenceHistory, role: Project['role'], principal?: string): HostAction[] {
  const state = host.fence_state ?? ''
  if (role === 'admin') {
    if (state === '') return ['request', host.draining ? 'resume' : 'drain']
    if (state === 'fenced') return ['request-rejoin']
  }
  if (role !== 'approver' || !principal) return []
  const latest = events.find((event) => event.revision === host.revision && event.generation === host.generation)
  if (!latest || latest.actor === principal) return []
  if (state === 'pending' && latest.action === 'request') return ['confirm']
  if (state === 'rejoin_pending' && latest.action === 'request-rejoin') return ['approve-rejoin', 'reject-rejoin']
  return []
}
