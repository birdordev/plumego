import { projectCanExecute } from '../../features/session/project'
import type { Session } from '../../features/session/store'
import { adminProps, consoleFlags, isAdmin, noProps, projectProps } from './project-context'
import { projectPage } from './project-page'

function hasConsole(session: Session) {
  const { nativeConsole, nativeSerial } = consoleFlags(session)
  return nativeConsole || nativeSerial
}

export const resourceRoutes = {
  overview: projectPage('项目总览', () => import('../../features/overview/OverviewPage'),
    (context) => ({ ...projectProps(context), ...adminProps(context),
      link: context.link, expireSession: context.expireSession,
    })),
  'cloud-accounts': projectPage('云账号管理', () => import('../../features/accounts/AccountsPage'),
    (context) => ({ ...projectProps(context), project: context.session.project.id,
      projects: context.session.profile.projects.filter(({ id, role }) => role === 'admin' && id !== context.session.project.id).map(({ id }) => id),
    }), { allowed: isAdmin }),
  assets: projectPage('资源清单', () => import('../../features/assets/inventory/AssetsPage'),
    ({ session, openSelectedBatch }) => ({
      canExecute: projectCanExecute(session), ...consoleFlags(session), onBatch: openSelectedBatch,
    }), {
      loadCollection: () => import('../../features/assets/inventory/collection').then(({ assetsCollection }) => assetsCollection),
    }),
  kvm: projectPage('KVM 运维', () => import('../../features/kvm/KVMPage'),
    ({ session }) => ({ role: session.project.role, principal: session.profile.principal }), {
      loadCollection: () => import('../../features/kvm/collection').then(({ kvmCollection }) => kvmCollection),
    }),
  'service-catalog': projectPage('服务目录', () => import('../../features/catalog/CatalogPage'), adminProps, {
    loadCollection: () => import('../../features/catalog/collection').then(({ catalogCollection }) => catalogCollection),
  }),
  'console-grants': projectPage('原生控制台', () => import('../../features/native-console/grants/GrantsPage'), noProps, {
    allowed: hasConsole,
    loadCollection: () => import('../../features/native-console/grants/collection').then(({ grantsCollection }) => grantsCollection),
  }),
}
