import { lazy, Suspense } from 'react'
import { openDetail } from '../../../shared/detail/store'
import type { Project } from '../../session/store'

const HostFencePanel = lazy(() => import('./HostFencePanel'))

export type FenceContext = {
  target: string; sitePath: string; role: Project['role']; backToHost: () => void
  onChanged: () => Promise<void>; openVM: (id: string, back: () => void) => void
}

export function openHostFence(context: FenceContext) {
  openDetail('宿主机隔离与重新接入', (stamp, signal) => <Suspense fallback={<p role="status">正在加载隔离工作台…</p>}>
    <HostFencePanel context={context} stamp={stamp} signal={signal} refresh={() => openHostFence(context)} />
  </Suspense>)
}
