import type { Session } from '../../features/session/store'
import { billingRoutes } from './billing'
import { governanceRoutes } from './governance'
import { operationRoutes } from './operations'
import { resourceRoutes } from './resources'

export const routes = {
  assets: resourceRoutes.assets,
  kvm: resourceRoutes.kvm,
  operations: operationRoutes.operations,
  'service-catalog': resourceRoutes['service-catalog'],
  alerts: governanceRoutes.alerts,
  'console-grants': resourceRoutes['console-grants'],
  batch: operationRoutes.batch,
  audit: operationRoutes.audit,
  billing: billingRoutes.billing,
  'billing-budget': billingRoutes['billing-budget'],
  'idle-instances': governanceRoutes['idle-instances'],
  'tag-compliance': governanceRoutes['tag-compliance'],
  'ticket-policy': governanceRoutes['ticket-policy'],
  'billing-allocation': billingRoutes['billing-allocation'],
}

export type View = keyof typeof routes
export type PageView = Exclude<View, 'batch'>
export const navigation = Object.keys(routes) as View[]

export function canOpenView(view: View, session: Session) {
  const route = routes[view]
  return !('allowed' in route) || (route.allowed?.(session) ?? true)
}

export function collectionLoaderFor(view: View) {
  const route = routes[view]
  return 'loadCollection' in route ? route.loadCollection : undefined
}
