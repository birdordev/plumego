import { lazy, Suspense } from 'react'
import { requestErrorMessage } from '../../../shared/api/requests'
import { openDetail } from '../../../shared/detail/store'
import { sessionSnapshot, type Project } from '../../session/store'
import { openColdMigrationReceipt } from '../cold-migration/open'
import { batchPath, planPath } from './api'
import { draftKey, readDraft } from './draft'
import type { Draft, Mode, Source } from './schema'

const EvacuationWorkspace = lazy(() => import('./EvacuationWorkspace'))
const EvacuationEditor = lazy(() => import('./EvacuationEditor'))
const EvacuationSubmission = lazy(() => import('./EvacuationSubmission'))
const PlanReceipt = lazy(() => import('./PlanReceipt'))
const BatchReceipt = lazy(() => import('./BatchReceipt'))

type Context = { target: string; sitePath: string; role: Project['role']; back: () => void }
const fallback = <p role="status">正在加载疏散工作台…</p>

export function openEvacuation(context: Context, initialTab: Mode = 'batch') {
  const principal = sessionSnapshot()?.profile.principal ?? ''
  const admin = context.role === 'admin' && principal !== ''
  const keyName = draftKey(context.target, principal)
  let draft: Draft | undefined
  let error: string | undefined
  try {
    if (admin) draft = readDraft(keyName)
  } catch (cause) {
    error = requestErrorMessage(cause)
  }
  openDetail('KVM 宿主机疏散', (_stamp, signal) => <Suspense fallback={fallback}>
    <EvacuationWorkspace target={context.target} signal={signal} initialTab={initialTab} refresh={(tab) => openEvacuation(context, tab)} back={context.back}
      draftError={error} restore={draft ? () => openSubmission(context, keyName, draft) : undefined}
      start={admin && !draft && !error ? (source) => openEditor(context, keyName, source) : undefined}
      open={(mode, id) => openReceipt(context, mode, id)} />
  </Suspense>)
}

function openEditor(context: Context, keyName: string, source: Source) {
  openDetail(source.mode === 'plan' ? '登记疏散预案' : '申请疏散批次', (stamp, signal) => <Suspense fallback={fallback}>
    <EvacuationEditor target={context.target} sitePath={context.sitePath} source={source} keyName={keyName} signal={signal} stamp={stamp}
      back={() => openEvacuation(context, source.mode)} review={(draft) => openSubmission(context, keyName, draft)} />
  </Suspense>)
}

function openSubmission(context: Context, keyName: string, draft: Draft) {
  openDetail('核对疏散清单', (stamp, signal) => <Suspense fallback={fallback}>
    <EvacuationSubmission target={context.target} keyName={keyName} draft={draft} signal={signal} stamp={stamp}
      back={() => openEvacuation(context, draft.mode)} onSaved={(id) => openReceipt(context, draft.mode, id)} />
  </Suspense>)
}

function openReceipt(context: Context, mode: Mode, id: string) {
  const back = () => openEvacuation(context, mode)
  if (mode === 'batch') {
    openBatchReceipt(context, id, back, '返回疏散工作台')
    return
  }
  const refresh = () => openReceipt(context, 'plan', id)
  openDetail('KVM 疏散预案回执', (stamp, signal) => <Suspense fallback={fallback}>
    <PlanReceipt target={planPath(context.target, id)} admin={context.role === 'admin'} stamp={stamp} signal={signal}
      back={back} refresh={refresh} openBatch={(batch) => openBatchReceipt(context, batch, refresh, '返回疏散预案')} />
  </Suspense>)
}

function openBatchReceipt(context: Context, id: string, back: () => void, backLabel: string) {
  const refresh = () => openBatchReceipt(context, id, back, backLabel)
  openDetail('KVM 疏散批次回执', (_stamp, signal) => <Suspense fallback={fallback}>
    <BatchReceipt target={batchPath(context.target, id)} signal={signal} refresh={refresh} back={back} backLabel={backLabel}
      openVM={(vm, migration) => openColdMigrationReceipt({ target: context.sitePath + '/vms/' + encodeURIComponent(vm),
        sitePath: context.sitePath, role: context.role }, migration, refresh, '返回疏散批次')} />
  </Suspense>)
}
