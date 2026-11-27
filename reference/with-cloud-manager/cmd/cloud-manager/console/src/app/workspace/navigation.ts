import type { ConsoleIconName } from '../../shared/components/ConsoleIcon'
import type { View } from '../routes/registry'

export const navigationGroups = [
  { views: ['overview'] },
  { label: '资源', views: ['assets', 'cloud-accounts', 'kvm', 'service-catalog', 'console-grants'] },
  { label: '运维', views: ['operations', 'batch', 'alerts', 'inventory-freshness', 'idle-instances'] },
  { label: '治理', views: ['bucket-protection', 'tag-compliance', 'ticket-policy', 'audit'] },
  { label: '费用', views: ['billing-summary', 'billing', 'project-budget', 'billing-budget', 'billing-allocation'] },
] as const satisfies readonly { label?: string; views: readonly View[] }[]

export const viewPresentation = {
  overview: { icon: 'overview', intro: '查看当前项目的盘点覆盖、近期操作和待处理告警。' },
  assets: { icon: 'assets', intro: '查找项目可见的云资源，查看盘点状态和可执行操作。' },
  'cloud-accounts': { icon: 'accounts', intro: '管理账号凭据引用、项目共享和定时盘点。' },
  kvm: { icon: 'server', intro: '查看虚拟机、宿主机、站点容量和运维操作。' },
  'service-catalog': { icon: 'catalog', intro: '按项目批准的规格和网络配置申请资源。' },
  'console-grants': { icon: 'terminal', intro: '查看并处理原生控制台和串行终端访问申请。' },
  operations: { icon: 'activity', intro: '追踪资源变更、审批进度和执行结果。' },
  batch: { icon: 'batch', intro: '预览并提交多资源操作，查看每项执行结果。' },
  alerts: { icon: 'bell', intro: '查阅项目告警及对应的处理状态。' },
  'inventory-freshness': { icon: 'time', intro: '查看盘点覆盖率、调度积压和各范围的最近完成时间。' },
  'idle-instances': { icon: 'gauge', intro: '核对实例近期负载和样本覆盖，识别闲置候选。' },
  'tag-compliance': { icon: 'shield', intro: '按资源类型检查必填标签和观测证据。' },
  'bucket-protection': { icon: 'shield', intro: '查看默认加密、版本控制及观测覆盖。' },
  'ticket-policy': { icon: 'ticket', intro: '管理变更工单要求及当前策略版本。' },
  audit: { icon: 'audit', intro: '查询项目内的授权、资源变更和访问事件。' },
  billing: { icon: 'bill', intro: '按账号、账期和版本查询云费用。' },
  'billing-summary': { icon: 'bill', intro: '按账期核对项目原币种费用和账单覆盖。' },
  'billing-budget': { icon: 'budget', intro: '查看预算、用量和费用阈值。' },
  'project-budget': { icon: 'budget', intro: '为选定云账号设置合计额度，查看各账号费用与缺账状态。' },
  'billing-allocation': { icon: 'allocation', intro: '维护费用分摊规则并读取已归集金额。' },
} as const satisfies Record<View, { icon: ConsoleIconName; intro: string }>
