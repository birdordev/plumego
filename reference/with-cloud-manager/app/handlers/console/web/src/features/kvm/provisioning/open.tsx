import { lazy, Suspense } from 'react'
import { requestErrorMessage } from '../../../shared/api/requests'
import { openDetail } from '../../../shared/detail/store'
import { kvmSitePath } from '../api'
import { draftKey, readDraft } from './draft'
import type { Draft, HostOption } from './schema'

const ProvisioningHosts = lazy(() => import('./ProvisioningHosts'))
const ProvisioningForm = lazy(() => import('./ProvisioningForm'))
const ProvisioningProgress = lazy(() => import('./ProvisioningProgress'))

type Context = { base: string; principal: string; site?: string; openVM: (site: string, vm: string) => void }

export function openProvisioning(context: Context) {
  const storageKey = draftKey(context.base, context.principal)
  let draft: Draft | undefined
  try {
    draft = readDraft(storageKey)
  } catch (error) {
    openDetail('创建 KVM 虚拟机', () => <p role="alert">{requestErrorMessage(error)}</p>)
    return
  }
  if (draft) {
    openProgress(context, storageKey, draft)
    return
  }
  openDetail('选择 KVM 宿主机', (_stamp, signal) => <Suspense fallback={<p role="status">正在加载宿主机…</p>}>
    <ProvisioningHosts base={context.base} site={context.site} signal={signal} refresh={() => openProvisioning(context)}
      select={(host) => openConfig(context, storageKey, host)} />
  </Suspense>)
}

function openConfig(context: Context, storageKey: string, host: HostOption) {
  openDetail('配置 KVM 虚拟机', (stamp, signal) => <Suspense fallback={<p role="status">正在加载创建表单…</p>}>
    <ProvisioningForm host={host} sitePath={kvmSitePath(context.base, host.site)} storageKey={storageKey} stamp={stamp} signal={signal}
      back={() => openProvisioning(context)} next={(draft) => openProgress(context, storageKey, draft)} />
  </Suspense>)
}

function openProgress(context: Context, storageKey: string, draft: Draft) {
  openDetail('KVM 虚拟机创建与预留', (stamp, signal) => <Suspense fallback={<p role="status">正在加载申请状态…</p>}>
    <ProvisioningProgress draft={draft} storageKey={storageKey} sitePath={kvmSitePath(context.base, draft.site)} stamp={stamp} signal={signal}
      resume={(next) => openProgress(context, storageKey, next)} restart={() => openProvisioning(context)}
      openVM={(id) => context.openVM(draft.site, id)} />
  </Suspense>)
}
