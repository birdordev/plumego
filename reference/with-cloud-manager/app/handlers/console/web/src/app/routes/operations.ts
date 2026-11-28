import { consoleFlags, isAdmin, noProps } from './project-context'
import { projectPage } from './project-page'

export const operationRoutes = {
  operations: projectPage('操作记录', () => import('../../features/operations/history/OperationsPage'), noProps, {
    loadCollection: () => import('../../features/operations/history/collection').then(({ operationsCollection }) => operationsCollection),
  }),
  batch: { title: '批量操作' },
  audit: projectPage('项目审计', () => import('../../features/audit/AuditPage'), ({ session }) => consoleFlags(session), {
    allowed: isAdmin,
    loadCollection: () => import('../../features/audit/collection').then(({ auditCollection }) => auditCollection),
  }),
}
