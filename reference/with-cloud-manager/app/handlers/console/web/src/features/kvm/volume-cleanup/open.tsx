import { lazy, Suspense } from 'react'
import { openDetail } from '../../../shared/detail/store'
import type { Project } from '../../session/store'
import { volumeCleanupsPath } from './api'
import type { Cleanup, RetainedVolume } from './schema'

const VolumeList = lazy(() => import('./VolumeList'))
const CleanupList = lazy(() => import('./CleanupList'))
const CleanupRequest = lazy(() => import('./CleanupRequest'))
const CleanupReceipt = lazy(() => import('./CleanupReceipt'))

type Context = { target: string; sitePath: string; role: Project['role']; back: () => void }
type JobKey = Pick<Cleanup, 'cleanup_id' | 'placement_id'>

export function openVMVolumes(context: Context) {
  openDetail('虚拟机保留卷', (_stamp, signal) => <Suspense fallback={<p role="status">正在读取保留卷…</p>}>
    <VolumeList target={context.target} signal={signal} back={context.back} refresh={() => openVMVolumes(context)}
      select={(volume) => openHistory(context, volume)} />
  </Suspense>)
}

export function openHostCleanups(context: Context & { backLabel?: string }) {
  openDetail('宿主机保留卷清理', (_stamp, signal) => <Suspense fallback={<p role="status">正在读取宿主机清理任务…</p>}>
    <CleanupList target={context.target + '/volume-cleanups'} signal={signal} back={context.back} backLabel={context.backLabel ?? '返回宿主机'}
      refresh={() => openHostCleanups(context)} select={(item) => openReceipt(context, item, () => openHostCleanups(context))} />
  </Suspense>)
}

function openHistory(context: Context, volume: RetainedVolume) {
  const target = volumeCleanupsPath(context.sitePath, volume.placement_id)
  openDetail('保留卷清理历史', (_stamp, signal) => <Suspense fallback={<p role="status">正在读取清理历史…</p>}>
    <CleanupList target={target} signal={signal} volume={volume} back={() => openVMVolumes(context)} backLabel="返回保留卷"
      refresh={() => openHistory(context, volume)} select={(item) => openReceipt(context, item, () => openHistory(context, volume))}
      start={context.role === 'admin' && volume.requestable ? () => openRequest(context, volume, target) : undefined} />
  </Suspense>)
}

function openRequest(context: Context, volume: RetainedVolume, target: string) {
  openDetail('申请保留卷清理', (stamp, signal) => <Suspense fallback={<p role="status">正在加载申请表单…</p>}>
    <CleanupRequest target={target} sitePath={context.sitePath} volume={volume} stamp={stamp} signal={signal}
      back={() => openHistory(context, volume)} onSaved={(job) => openReceipt(context, job, () => openHistory(context, volume))} />
  </Suspense>)
}

function openReceipt(context: Context, job: JobKey, back: () => void) {
  const target = volumeCleanupsPath(context.sitePath, job.placement_id) + '/' + encodeURIComponent(job.cleanup_id)
  openDetail('保留卷清理回执', (stamp, signal) => <Suspense fallback={<p role="status">正在读取清理回执…</p>}>
    <CleanupReceipt target={target} sitePath={context.sitePath} role={context.role} stamp={stamp} signal={signal}
      back={back} refresh={() => openReceipt(context, job, back)} />
  </Suspense>)
}
