import { lazy, Suspense } from 'react'
import { openDetail } from '../../../shared/detail/store'
import type { Project } from '../../session/store'
import type { VMDetail } from '../schema'

const MigrationHistory = lazy(() => import('./MigrationHistory'))
const MigrationRequest = lazy(() => import('./MigrationRequest'))
const MigrationReceipt = lazy(() => import('./MigrationReceipt'))

export type MigrationContext = {
  target: string; sitePath: string; vm: VMDetail; role: Project['role']; backToVM: () => void
}

export function openLiveMigrations(context: MigrationContext) {
  openDetail('KVM 实时迁移', (_stamp, signal) => <Suspense fallback={<p role="status">正在加载迁移记录…</p>}>
    <MigrationHistory context={context} signal={signal} refresh={() => openLiveMigrations(context)}
      start={() => openMigrationRequest(context)} select={(id) => openMigrationReceipt(context, id, () => openLiveMigrations(context))} />
  </Suspense>)
}

function openMigrationRequest(context: MigrationContext) {
  openDetail('申请实时迁移', (stamp, signal) => <Suspense fallback={<p role="status">正在加载候选宿主机…</p>}>
    <MigrationRequest target={context.target} vm={context.vm} stamp={stamp} signal={signal}
      back={() => openLiveMigrations(context)} refresh={() => openMigrationRequest(context)}
      onSaved={(id) => openMigrationReceipt(context, id, () => openLiveMigrations(context))} />
  </Suspense>)
}

export function openMigrationReceipt(context: Pick<MigrationContext, 'target' | 'sitePath' | 'role'> & { backLabel?: string }, id: string, back: () => void) {
  const target = context.target + '/live-migrations/' + encodeURIComponent(id)
  openDetail('KVM 实时迁移回执', (stamp, signal) => <Suspense fallback={<p role="status">正在加载迁移回执…</p>}>
    <MigrationReceipt target={target} sitePath={context.sitePath} role={context.role} stamp={stamp} signal={signal}
      back={back} backLabel={context.backLabel} refresh={() => openMigrationReceipt(context, id, back)} />
  </Suspense>)
}
