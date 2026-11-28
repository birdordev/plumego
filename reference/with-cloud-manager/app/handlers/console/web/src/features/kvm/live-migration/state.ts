import type { Project } from '../../session/store'
import type { VMDetail } from '../schema'
import type { Migration } from './schema'

export const statuses = {
  pending: '待审批', approved: '已批准', dispatched: '已派发', source_departed: '源端已离开',
  completed: '已完成', rejected: '已拒绝', aborted: '已终止',
} satisfies Record<Migration['status'], string>

export function canRequest(role: Project['role'], vm: VMDetail) {
  return role === 'admin' && vm.state === 'running' && vm.observation === 'running' &&
    Boolean(vm.shared_storage_pool_id && vm.image_id) && !vm.ipv4 && vm.capacity.gpu === 0
}

export function isActive(status: Migration['status']) {
  return status === 'pending' || status === 'approved' || status === 'dispatched' || status === 'source_departed'
}
