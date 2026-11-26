import { useEffect, useRef, useState, type FormEvent } from 'react'
import { ConsoleRequestError, requestErrorMessage } from '../../../shared/api/requests'
import { saveTicketPolicy } from './api'
import { policyInput, type TicketPolicy } from './schema'

type Props = { target: string; initial?: TicketPolicy; reload: () => void }

export function TicketPolicyEditor({ target, initial, reload }: Props) {
  const [policy, setPolicy] = useState(initial)
  const [busy, setBusy] = useState(false)
  const [notice, setNotice] = useState<{ text: string; error?: boolean }>()
  const pending = useRef<AbortController>(null)
  useEffect(() => () => pending.current?.abort(), [])

  async function publish(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (pending.current) return
    const form = new FormData(event.currentTarget)
    const actions = String(form.get('actions') ?? '').split(/\s*,\s*|\s+/).filter(Boolean)
    const input = policyInput.safeParse({ actions, verifier: String(form.get('verifier') ?? '').trim() })
    if (!input.success || (!policy && !actions.length)) {
      setNotice({ text: '请填写核验主体和有效的动作。全选使用单独的 *，已发布策略可清空动作停用。', error: true })
      return
    }
    const controller = new AbortController()
    pending.current = controller
    setBusy(true)
    setNotice(undefined)
    try {
      const saved = await saveTicketPolicy(target, input.data, policy?.revision, controller.signal)
      if (!controller.signal.aborted) {
        setPolicy(saved)
        setNotice({ text: '已发布工单策略。' })
      }
    } catch (error) {
      if (!controller.signal.aborted) {
        const conflict = error instanceof ConsoleRequestError && (error.status === 409 || error.status === 412)
        setNotice({ text: conflict ? '策略版本已变更。请核对草稿，重新读取后再发布。' : requestErrorMessage(error), error: true })
      }
    } finally {
      pending.current = null
      if (!controller.signal.aborted) setBusy(false)
    }
  }

  return <form className="filters" aria-busy={busy} onSubmit={(event) => void publish(event)}>
    <p className="hint">{policy ? `当前版本 ${policy.revision} · ${policy.actions.length ? '已启用' : '已停用'}` : '项目尚未发布工单策略。'}</p>
    <label>核验服务主体<input name="verifier" defaultValue={policy?.verifier} maxLength={128} disabled={busy} /></label>
    <label>需要工单的动作
      <textarea name="actions" rows={5} defaultValue={policy?.actions.join('\n')} maxLength={16384} disabled={busy} />
    </label>
    <p className="hint">每行输入一个动作。单独输入 * 覆盖全部动作。清空已发布策略的动作可停用新申请的工单要求。</p>
    <div className="row-actions">
      <button type="submit" className="primary" disabled={busy}>发布工单策略</button>
      <button type="button" disabled={busy} onClick={reload}>重新读取策略</button>
    </div>
    {notice ? <p role={notice.error ? 'alert' : 'status'} className="notice" data-error={Boolean(notice.error)}>{notice.text}</p> : null}
  </form>
}
