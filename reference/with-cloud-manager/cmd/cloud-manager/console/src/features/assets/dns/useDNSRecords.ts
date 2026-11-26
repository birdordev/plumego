import { useCallback, useEffect, useRef, useState } from 'react'
import { ConsoleRequestError, requestErrorMessage } from '../../../shared/api/requests'
import { request } from '../../session/client'
import { dnsPageSchema } from './schema'
import type { DNSAsset, DNSRecord } from './types'

type Snapshot = { observedAt: string; total: number; next?: number; records: DNSRecord[] }
const emptySnapshot: Snapshot = { observedAt: '', total: 0, records: [] }

export function useDNSRecords(base: string, { id, account }: DNSAsset, signal: AbortSignal) {
  const current = useRef<AbortController | null>(null)
  const [snapshot, setSnapshot] = useState(emptySnapshot)
  const [changed, setChanged] = useState(false)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')

  const load = useCallback(async (offset: number, previous?: Snapshot) => {
    current.current?.abort()
    const controller = new AbortController()
    current.current = controller
    const readSignal = AbortSignal.any([signal, controller.signal])
    setLoading(true)
    setError('')
    const query = new URLSearchParams({ account, offset: String(offset) })
    try {
      const data = await request(
        base + '/assets/dns-zones/' + encodeURIComponent(id) + '/records?' + query,
        { signal: readSignal }, dnsPageSchema,
      )
      if (readSignal.aborted) return
      if (previous && previous.observedAt !== data.observed_at) {
        setChanged(true)
        return
      }
      setChanged(false)
      setSnapshot({
        observedAt: data.observed_at,
        total: data.total,
        next: data.next_offset,
        records: previous ? previous.records.concat(data.records) : data.records,
      })
    } catch (error) {
      if (readSignal.aborted) return
      setError(error instanceof ConsoleRequestError && error.status === 404
        ? '暂时没有可用的 DNS 快照。' : requestErrorMessage(error))
    } finally {
      if (!readSignal.aborted) setLoading(false)
    }
  }, [account, base, id, signal])

  useEffect(() => {
    void load(0)
    return () => current.current?.abort()
  }, [load])

  return {
    snapshot, loading, changed, error,
    reload: () => load(0),
    loadNext: () => snapshot.next ? load(snapshot.next, snapshot) : undefined,
  }
}
