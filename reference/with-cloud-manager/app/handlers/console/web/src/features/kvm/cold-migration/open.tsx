import { lazy, Suspense } from 'react'
import { requestErrorMessage } from '../../../shared/api/requests'
import { openDetail } from '../../../shared/detail/store'
import { sessionSnapshot, type Project } from '../../session/store'
import { migrationKey, readAttempt } from './attempt'
import type { Attempt, Eligibility } from './schema'

const MigrationHistory = lazy(() => import('./MigrationHistory'))
const MigrationForm = lazy(() => import('./MigrationForm'))
const MigrationSubmission = lazy(() => import('./MigrationSubmission'))
const MigrationReceipt = lazy(() => import('./MigrationReceipt'))

export type Context = { base: string; site: string; sitePath: string; target: string; role: Project['role']; backToVM: () => void }
export type ReceiptContext = Pick<Context, 'target' | 'sitePath' | 'role'>

export function openColdMigrations(context: Context) {
  const principal = sessionSnapshot()?.profile.principal ?? ''
  const keyName = migrationKey(context.target, principal)
  const admin = context.role === 'admin' && principal !== ''
  let pending: Attempt | undefined
  let draftError: string | undefined
  try {
    if (admin) pending = readAttempt(keyName)
  } catch (error) {
    draftError = requestErrorMessage(error)
  }
  openDetail('KVM 冷迁移与单机疏散', (_stamp, signal) => <Suspense fallback={<p role="status">正在加载迁移记录…</p>}>
    <MigrationHistory context={context} pending={pending ? () => openSubmission(context, keyName, pending) : undefined}
      signal={signal} draftError={draftError} refresh={() => openColdMigrations(context)}
      start={admin && !pending && !draftError ? (source) => openForm(context, keyName, source) : undefined}
      select={(id) => openReceipt(context, id)} />
  </Suspense>)
}

function openForm(context: Context, keyName: string, source: Exclude<Eligibility, { mode: 'unavailable' }>) {
  openDetail(source.mode === 'cold' ? '申请冷迁移' : '申请单机疏散', (stamp, signal) => <Suspense fallback={<p role="status">正在加载恢复表单…</p>}>
    <MigrationForm base={context.base} site={context.site} sitePath={context.sitePath} source={source} keyName={keyName}
      stamp={stamp} signal={signal} back={() => openColdMigrations(context)} prepare={(draft) => openSubmission(context, keyName, draft)} />
  </Suspense>)
}

function openSubmission(context: Context, keyName: string, attempt: Attempt) {
  openDetail('核对 KVM 迁移申请', (stamp, signal) => <Suspense fallback={<p role="status">正在读取原申请…</p>}>
    <MigrationSubmission target={context.target} sitePath={context.sitePath} keyName={keyName} attempt={attempt} stamp={stamp} signal={signal}
      back={() => openColdMigrations(context)} onSaved={(receipt) => openReceipt(context, receipt.migration_id)} />
  </Suspense>)
}

function openReceipt(context: Context, id: string) {
  openColdMigrationReceipt(context, id, () => openColdMigrations(context), '返回迁移记录')
}

export function openColdMigrationReceipt(context: ReceiptContext, id: string, back: () => void, backLabel: string) {
  openDetail('KVM 冷迁移回执', (stamp, signal) => <Suspense fallback={<p role="status">正在加载迁移回执…</p>}>
    <MigrationReceipt context={context} id={id} stamp={stamp} signal={signal}
      back={back} backLabel={backLabel} refresh={() => openColdMigrationReceipt(context, id, back, backLabel)} />
  </Suspense>)
}
