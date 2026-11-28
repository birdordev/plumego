import type { HostSummary } from './schema'

const vmStates = new Map(Object.entries({
  awaiting_approval: '创建待审批', rejected: '创建已拒绝', queued: '创建已排队', staging: '正在准备启动卷',
  staged: '启动卷已准备', defining: '正在定义',
  defined: '待启动', starting: '正在启动', running: '运行中', shutdown_requested: '关机待审批',
  shutdown_queued: '关机已排队', stopping: '正在关机', stopped: '已关机', retire_requested: '退役待审批',
  retire_queued: '退役已排队', undefining: '正在退役', retired: '已退役',
}))

export function vmStatus(state: string) {
  return vmStates.get(state) ?? state
}

export function hostStatus(host: HostSummary) {
  switch (host.fence_state) {
    case 'pending': return '待确认隔离'
    case 'fenced': return '已隔离'
    case 'rejoin_pending': return '重新接入待审批'
    default:
      if (host.draining) return '停止接收新放置'
      return host.reported_at ? '已上报容量' : '待上报容量'
  }
}
