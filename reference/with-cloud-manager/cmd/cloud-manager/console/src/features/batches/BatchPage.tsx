import { useImperativeHandle, useState, type Ref } from 'react'
import { BatchReceiptLookup } from './BatchReceiptLookup'
import { BatchPreviewReport, BatchReceiptReport } from './BatchReport'
import { BatchRequestForm } from './BatchRequestForm'
import { formatSelectedInstances } from './selection-format'
import { useBatchEditor } from './useBatchEditor'
import './styles.css'

export type BatchEditor = { fill: () => void }

type Props = { base: string; canExecute: boolean; initialInput: string; ref?: Ref<BatchEditor> }

export default function BatchPage({ base, canExecute, initialInput, ref }: Props) {
  const { input, id, busy, ready, report, notice, edit, setID, preview, submit, lookup } = useBatchEditor(base, initialInput)
  const [action, setAction] = useState('start')
  function fill() {
    if (canExecute && !busy) edit(formatSelectedInstances(action))
  }
  useImperativeHandle(ref, () => ({ fill }))

  return (
    <section id="batch" aria-labelledby="title" aria-busy={busy}>
      <BatchRequestForm canExecute={canExecute} input={input} busy={busy} ready={ready}
        edit={edit} preview={preview} submit={submit} action={action} setAction={setAction} fill={fill} />
      <p id="batch-message" className="notice" role="status" data-error={Boolean(notice?.error)}>{notice?.text}</p>
      <div id="batch-report" className="batch-report" hidden={!report}>
        {report && ('preview' in report ? <BatchPreviewReport preview={report.preview} /> : <BatchReceiptReport receipt={report.receipt} />)}
      </div>
      <BatchReceiptLookup id={id} busy={busy} setID={setID} lookup={lookup} />
    </section>
  )
}
