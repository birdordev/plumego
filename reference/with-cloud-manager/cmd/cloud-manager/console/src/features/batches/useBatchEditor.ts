import { useState } from 'react'
import { clearSelection } from '../assets/instances/selection-store'
import { previewBatch } from './preview/api'
import type { BatchPreview } from './preview/schema'
import { lookupBatch } from './receipt/api'
import type { BatchReceipt } from './receipt/schema'
import { useBatchSubmission } from './receipt/useBatchSubmission'
import { batchInputError } from './request/input'
import { useBatchRead } from './useBatchRead'

type Report = { preview: BatchPreview } | { receipt: BatchReceipt }
type Notice = { text: string; error?: boolean }

export function useBatchEditor(base: string, initialInput: string) {
  const [input, setInput] = useState(initialInput)
  const [id, setID] = useState('')
  const [ready, setReady] = useState(false)
  const [report, setReport] = useState<Report>()
  const [notice, setNotice] = useState<Notice>()
  const showError = (text: string) => setNotice({ text, error: true })
  const { read, reading, cancel } = useBatchRead(showError)
  const submission = useBatchSubmission(base, (receipt) => {
    setReady(false)
    setID(receipt.id)
    setReport({ receipt })
    setNotice({ text: '批次已受理，请查看各项状态。' })
    clearSelection()
  }, showError)

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
      const { uncertain } = submission.prepare(input)
      setReport({ preview: result })
      setReady(result.can_submit || uncertain)
      setNotice(uncertain
        ? { text: '上次提交结果待确认。可用原幂等键重取收据。' }
        : { text: result.can_submit ? '预览通过，可以提交。' : '请处理阻塞项或缩小批次。', error: !result.can_submit })
    })
  }

  async function submit() {
    if (ready) await submission.submit(input)
  }

  function lookup() {
    if (!id.trim()) return
    setReady(false)
    setReport(undefined)
    setNotice(undefined)
    void read((signal) => lookupBatch(base, id.trim(), signal), (receipt) => setReport({ receipt }))
  }

  return { input, id, busy: reading || submission.saving, ready, report, notice, edit, setID, preview, submit, lookup }
}
