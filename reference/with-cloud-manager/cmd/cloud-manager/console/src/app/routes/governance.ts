import { adminProps, isAdmin, noProps, projectProps } from './project-context'
import { projectPage } from './project-page'

export const governanceRoutes = {
  'bucket-protection': projectPage('对象桶防护', () => import('../../features/governance/buckets/BucketProtectionPage'),
    (context) => ({
      ...projectProps(context), filters: context.filters, commitFilters: context.commitFilters,
      assetLink: ({ id, account }: { id: string; account: string }) =>
        context.link('assets', new URLSearchParams({ q: id, account, kind: 'object-bucket' }).toString()),
    })),
  'inventory-freshness': projectPage('盘点新鲜度', () => import('../../features/governance/freshness/InventoryFreshnessPage'),
    (context) => ({ ...projectProps(context), filters: context.filters, commitFilters: context.commitFilters })),
  alerts: projectPage('项目告警', () => import('../../features/alerts/AlertsPage'), adminProps, {
    loadCollection: () => import('../../features/alerts/collection').then(({ alertsCollection }) => alertsCollection),
  }),
  'idle-instances': projectPage('实例闲置分析', () => import('../../features/governance/idle/IdleInstancesPage'), noProps, {
    loadCollection: () => import('../../features/governance/idle/collection').then(({ idleCollection }) => idleCollection),
  }),
  'tag-compliance': projectPage('标签合规', () => import('../../features/governance/tags/TagCompliancePage'),
    (context) => ({ ...projectProps(context), ...adminProps(context) })),
  'ticket-policy': projectPage('工单策略', () => import('../../features/governance/change-tickets/TicketPolicyPage'),
    projectProps, { allowed: isAdmin }),
}
