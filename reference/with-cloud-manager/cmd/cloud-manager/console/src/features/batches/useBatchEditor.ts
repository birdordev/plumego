import { useRef, useState } from 'react'
import { ConsoleRequestError } from '../../shared/api/requests'
import { sessionSnapshot } from '../session/store'
import { lookupBatch, previewBatch, submitBatch } from './api'
import { batchInputError } from './input'
import type { BatchPreview, BatchReceipt } from './schema'
import { clearSelection } from '../assets/instances/selection-store'
import { useBatchRead } from './useBatchRead'

type Report = { preview: BatchPreview } | { receipt: BatchReceipt }
type Notice = { text: string; error?: boolean }
type Attempt = { key: string; uncertain: boolean }

export function useBatchEditor(base: string, initialInput: string) {
  const [input, setInput] = useState(initialInput)
  const [id, setID] = useState('')
  const [saving, setSaving] = useState(false)
  const [ready, setReady] = useState(false)
  const [report, setReport] = useState<Report>()
  const [notice, setNotice] = useState<Notice>()
  const { read, reading, cancel } = useBatchRead((text) => setNotice({ text, error: true }))
  const submitting = useRef(false)
  const previous = useRef<{ payload: string; attempt: Attempt } | null>(null)

  function edit(next: string) {
    cancel()
    setInput(next)
    setReady(false)
    setReport(undefined)
    setNotice(undefined)
  }

  async function preview() {
    const error = batchInputError(input)
    setReady(false)
    setReport(undefined)
    if (error) {
      setNotice({ text: error, error: true })
      return
    }
    setNotice(undefined)
    await read((signal) => previewBatch(base, input, signal), (result) => {
      const old = previous.current
      const attempt = old?.payload === input ? old.attempt : { key: crypto.randomUUID(), uncertain: false }
      previous.current = { payload: input, attempt }
      setReport({ preview: result })
      setReady(result.can_submit || attempt.uncertain)
      setNotice(attempt.uncertain
        ? { text: '上次提交结果待确认。可用原幂等键重取收据。' }
        : { text: result.can_submit ? '预览通过，可以提交。' : '请处理阻塞项或缩小批次。', error: !result.can_submit })
    })
  }

  async function submit() {
    const entry = previous.current
    if (!ready || submitting.current || entry?.payload !== input) return
    const session = sessionSnapshot()
    submitting.current = true
    setSaving(true)
    try {
      const receipt = await submitBatch(base, input, entry.attempt.key)
      if (sessionSnapshot() !== session) return
      setReady(false)
      setID(receipt.id)
      setReport({ receipt })
      setNotice({ text: '批次已受理，请查看各项状态。' })
      clearSelection()
    } catch (error) {
      entry.attempt.uncertain = !(error instanceof ConsoleRequestError) || error.status >= 500
      if (sessionSnapshot() === session) setNotice({ text: error instanceof Error ? error.message : '提交批次失败。', error: true })
    } finally {
      submitting.current = false
      if (sessionSnapshot() === session) setSaving(false)
    }
  }

  function lookup() {
    if (!id.trim()) return
    setReady(false)
    setReport(undefined)
    setNotice(undefined)
    void read((signal) => lookupBatch(base, id.trim(), signal), (receipt) => setReport({ receipt }))
  }

  return { input, id, busy: reading || saving, ready, report, notice, edit, setID, preview, submit, lookup }
}
