import { useCallback, useEffect, useRef, useState } from 'react'
import { ConsoleRequestError } from '../../../shared/api/requests'
import type { ClusterAsset, ClusterSnapshot, ClusterSnapshotReader } from './snapshot'

export function useClusterSnapshot<T>(base: string, cluster: ClusterAsset, read: ClusterSnapshotReader<T>, label: string, signal: AbortSignal) {
  const active = useRef<AbortController | null>(null)
  const [page, setPage] = useState<ClusterSnapshot<T>>()
  const [busy, setBusy] = useState(true)
  const [changed, setChanged] = useState(false)
  const [error, setError] = useState('')
  const { account, region, id, name } = cluster

  const load = useCallback(async (previous?: ClusterSnapshot<T>) => {
    active.current?.abort()
    const controller = new AbortController()
    active.current = controller
    const readSignal = AbortSignal.any([signal, controller.signal])
    setBusy(true)
    setError('')
    try {
      const next = await read(base, { account, region, id, name }, readSignal, previous)
      if (readSignal.aborted) return
      if (previous && (previous.snapshot !== next.snapshot ||
          previous.total !== next.total || previous.observed_at !== next.observed_at)) {
        setChanged(true)
        return
      }
      const end = (previous?.items.length ?? 0) + next.items.length
      if (end > next.total || next.next_offset !== (end < next.total ? end : undefined) ||
          (next.items.length === 0 && end < next.total)) {
        throw new Error('服务返回的快照分页无效。')
      }
      setChanged(false)
      setPage(previous ? { ...next, items: previous.items.concat(next.items) } : next)
    } catch (failure) {
      if (readSignal.aborted) return
      if (failure instanceof ConsoleRequestError) {
        if (failure.status === 409 && previous) {
          setChanged(true)
          return
        }
        if ([401, 403, 404].includes(failure.status)) {
          setPage(undefined)
          setChanged(false)
        }
        if (failure.status === 404) {
          setError('暂时没有可用的' + label + '快照。')
          return
        }
      }
      setError(failure instanceof Error ? failure.message : '读取' + label + '失败。')
    } finally {
      if (!readSignal.aborted) setBusy(false)
    }
  }, [account, base, id, label, name, read, region, signal])

  useEffect(() => {
    void load()
    return () => active.current?.abort()
  }, [load])

  return { page, busy, changed, error, reload: () => load(), more: () => page?.next_offset && load(page) }
}
