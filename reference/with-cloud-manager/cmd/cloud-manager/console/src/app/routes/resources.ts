import { projectCanExecute } from '../../features/session/project'
import type { Session } from '../../features/session/store'
import { adminProps, consoleFlags, noProps } from './project-context'
import { projectPage } from './project-page'

function hasConsole(session: Session) {
  const { nativeConsole, nativeSerial } = consoleFlags(session)
  return nativeConsole || nativeSerial
}

export const resourceRoutes = {
  assets: projectPage('资源清单', () => import('../../features/assets/inventory/AssetsPage'),
    ({ session, openSelectedBatch }) => ({
      canExecute: projectCanExecute(session), ...consoleFlags(session), onBatch: openSelectedBatch,
    }), {
      loadCollection: () => import('../../features/assets/inventory/collection').then(({ assetsCollection }) => assetsCollection),
    }),
  'service-catalog': projectPage('服务目录', () => import('../../features/catalog/CatalogPage'), adminProps, {
    loadCollection: () => import('../../features/catalog/collection').then(({ catalogCollection }) => catalogCollection),
  }),
  'console-grants': projectPage('原生控制台', () => import('../../features/native-console/grants/GrantsPage'), noProps, {
    allowed: hasConsole,
    loadCollection: () => import('../../features/native-console/grants/collection').then(({ grantsCollection }) => grantsCollection),
  }),
}
