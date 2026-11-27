import { lazy, Suspense } from 'react'
import { openDetail } from '../../../shared/detail/store'
import type { Project } from '../../session/store'
import { detailPath, kvmSitePath } from '../api'
import { kvmCollection } from '../collection'
import { openHostFence } from '../host-fence/open'
import type { InventoryItem } from '../schema'
import { openHostCleanups } from '../volume-cleanup/open'

const HostPanel = lazy(() => import('./HostPanel'))
const VMPanel = lazy(() => import('./VMPanel'))

export function showInventoryDetail(base: string, item: InventoryItem, role: Project['role']) {
  const target = detailPath(base, item)
  const sitePath = kvmSitePath(base, item.site)
  if (item.kind === 'vms') {
    showVMByID(base, item.site, item.id, role)
    return
  }
  const reload = () => showInventoryDetail(base, item, role)
  function fence() {
    openHostFence({ target, sitePath, role, backToHost: reload, onChanged: kvmCollection.refresh,
      openVM: (id, back) => showVMByID(base, item.site, id, role, back),
    })
  }
  openDetail('KVM 宿主机详情', (_stamp, signal) => <Suspense fallback={<p role="status">正在加载…</p>}>
    <HostPanel target={target} signal={signal} reload={reload} onFence={fence}
      onCleanups={() => openHostCleanups({ target, sitePath, role, back: reload })} />
  </Suspense>)
}

export function showVMByID(base: string, site: string, id: string, role: Project['role'], back?: () => void) {
  const sitePath = kvmSitePath(base, site)
  const target = sitePath + '/vms/' + encodeURIComponent(id)
  const reload = () => showVMByID(base, site, id, role, back)
  openDetail('KVM 虚拟机详情', (stamp, signal) => <Suspense fallback={<p role="status">正在加载…</p>}>
    <VMPanel base={base} target={target} site={site} sitePath={sitePath} role={role} stamp={stamp} signal={signal}
      reload={reload} onChanged={kvmCollection.refresh} back={back} />
  </Suspense>)
}
