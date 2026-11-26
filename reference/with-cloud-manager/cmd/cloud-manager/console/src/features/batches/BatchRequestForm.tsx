import { useSyncExternalStore } from 'react'
import { selectionCount, subscribeSelection } from '../assets/selection-store'

type Props = {
  canExecute: boolean
  input: string
  busy: boolean
  ready: boolean
  edit: (input: string) => void
  preview: () => Promise<void>
  submit: () => Promise<void>
  action: string
  setAction: (action: string) => void
  fill: () => void
}

export function BatchRequestForm({ canExecute, input, busy, ready, edit, preview, submit, action, setAction, fill }: Props) {
  const count = useSyncExternalStore(subscribeSelection, selectionCount)
  return (
    <form id="batch-editor" hidden={!canExecute} onSubmit={(event) => { event.preventDefault(); void preview() }}>
      <div className="batch-top">
        <label>应用到已选实例<select id="batch-action" value={action} onChange={(event) => setAction(event.target.value)}>
          <option value="start">启动</option><option value="stop">停止</option>
          <option value="reboot">重启</option><option value="delete">删除</option>
        </select></label>
        <button id="fill-batch" onClick={fill} type="button" hidden={count === 0} disabled={busy}>填入所选实例</button>
      </div>
      <label>批次请求 JSON<textarea id="batch-input" value={input} onChange={(event) => edit(event.target.value)}
        disabled={busy} rows={12} spellCheck={false} aria-describedby="batch-help" /></label>
      <p id="batch-help" className="hint">每批最多 25 项。预览通过后可提交。</p>
      <div className="row-actions">
        <button id="preview-batch" type="submit" className="primary" disabled={busy}>预览批次</button>
        <button id="submit-batch" onClick={() => void submit()} type="button" disabled={busy || !ready}>提交批次</button>
      </div>
    </form>
  )
}
