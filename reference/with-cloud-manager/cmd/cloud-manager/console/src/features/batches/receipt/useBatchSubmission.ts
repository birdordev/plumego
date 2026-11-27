import { useRef, useState } from 'react'
import { ConsoleRequestError } from '../../../shared/api/requests'
import { sessionSnapshot } from '../../session/store'
import { submitBatch } from './api'
import type { BatchReceipt } from './schema'

type Attempt = { payload: string; key: string; uncertain: boolean }

export function useBatchSubmission(base: string, onReceipt: (receipt: BatchReceipt) => void, onError: (text: string) => void) {
  const [saving, setSaving] = useState(false)
  const inFlight = useRef(false)
  const previous = useRef<Attempt | undefined>(undefined)

  function prepare(payload: string) {
    const attempt = previous.current
    if (attempt?.payload === payload) return attempt
    const next = { payload, key: crypto.randomUUID(), uncertain: false }
    previous.current = next
    return next
  }

  async function submit(payload: string) {
    const attempt = previous.current
    if (inFlight.current || attempt?.payload !== payload) return

    const session = sessionSnapshot()
    inFlight.current = true
    setSaving(true)
    try {
      const receipt = await submitBatch(base, payload, attempt.key)
      if (sessionSnapshot() === session) onReceipt(receipt)
    } catch (error) {
      attempt.uncertain = !(error instanceof ConsoleRequestError) || error.status >= 500
      if (sessionSnapshot() === session) onError(error instanceof Error ? error.message : '提交批次失败。')
    } finally {
      inFlight.current = false
      if (sessionSnapshot() === session) setSaving(false)
    }
  }

  return { prepare, saving, submit }
}
