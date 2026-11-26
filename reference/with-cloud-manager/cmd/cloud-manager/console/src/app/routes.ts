import { lazy, type ComponentType } from 'react'
import type { Session } from '../features/session/store'
import type { CollectionLifecycle } from '../shared/collection/store'

export type PageProps = {
  base: string
  canExecute: boolean
  admin: boolean
  nativeConsole: boolean
  nativeSerial: boolean
  onBatch: () => void
}

type Route = { title: string; allowed?: (session: Session) => boolean }
type CollectionLoader = () => Promise<CollectionLifecycle>
type PageRoute = Route & { Page: ComponentType<PageProps>; loadCollection?: CollectionLoader }

const isAdmin = ({ project }: Session) => project.role === 'admin'
const hasConsole = ({ profile }: Session) => Boolean(profile.native_console || profile.native_serial_console)

export const routes = {
  assets: { title: '资源清单', Page: lazy(() => import('../features/assets/AssetsPage')),
    loadCollection: () => import('../features/assets/collection').then(({ assetsCollection }) => assetsCollection) },
  operations: { title: '操作记录', Page: lazy(() => import('../features/operations/OperationsPage')),
    loadCollection: () => import('../features/operations/collection').then(({ operationsCollection }) => operationsCollection) },
  'service-catalog': { title: '服务目录', Page: lazy(() => import('../features/catalog/CatalogPage')),
    loadCollection: () => import('../features/catalog/collection').then(({ catalogCollection }) => catalogCollection) },
  alerts: { title: '项目告警', Page: lazy(() => import('../features/alerts/AlertsPage')),
    loadCollection: () => import('../features/alerts/collection').then(({ alertsCollection }) => alertsCollection) },
  'console-grants': { title: '原生控制台', Page: lazy(() => import('../features/native-console/GrantsPage')),
    loadCollection: () => import('../features/native-console/collection').then(({ grantsCollection }) => grantsCollection), allowed: hasConsole },
  batch: { title: '批量操作' },
  audit: { title: '项目审计', Page: lazy(() => import('../features/audit/AuditPage')),
    loadCollection: () => import('../features/audit/collection').then(({ auditCollection }) => auditCollection), allowed: isAdmin },
  billing: { title: '云账单', Page: lazy(() => import('../features/billing/BillingPage')), allowed: isAdmin },
  'billing-budget': { title: '云预算', Page: lazy(() => import('../features/billing/budgets/BudgetsPage')), allowed: isAdmin },
  'idle-instances': { title: '实例闲置分析', Page: lazy(() => import('../features/governance/idle/IdleInstancesPage')),
    loadCollection: () => import('../features/governance/idle/collection').then(({ idleCollection }) => idleCollection) },
  'tag-compliance': { title: '标签合规', Page: lazy(() => import('../features/governance/tags/TagCompliancePage')) },
  'billing-allocation': { title: '费用分摊', Page: lazy(() => import('../features/billing/allocation/AllocationPage')), allowed: isAdmin },
} satisfies Record<string, PageRoute | (Route & { Page?: never; loadCollection?: never })>

export type View = keyof typeof routes
export type PageView = Exclude<View, 'batch'>
export const navigation = Object.keys(routes) as View[]

export function canOpenView(view: View, session: Session) {
  const route: Route = routes[view]
  return route.allowed?.(session) ?? true
}

export function collectionLoaderFor(view: View) {
  const route: { title: string; loadCollection?: CollectionLoader } = routes[view]
  return route.loadCollection
}
