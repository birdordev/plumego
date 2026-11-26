import { adminProps, isAdmin, noProps, projectProps } from './project-context'
import { projectPage } from './project-page'

export const governanceRoutes = {
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
