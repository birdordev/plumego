import { useEffect, useRef, useState, type FormEvent } from 'react'
import { ConsoleRequestError, requestErrorMessage } from '../../../shared/api/requests'
import { formText } from '../../../shared/detail/form-values'
import { projectBudgetInput, saveProjectBudget, type ProjectBudget } from './api'

export function ProjectBudgetEditor({ target, budget, onSaved }: { target: string; budget?: ProjectBudget; onSaved: () => void }) {
  const writing = useRef<AbortController>(undefined)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  useEffect(() => () => writing.current?.abort(), [])

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (writing.current) return
    const data = new FormData(event.currentTarget)
    const accounts = formText(data, 'accounts').split(/\r?\n/).map((item) => item.trim()).filter(Boolean)
    const parsed = projectBudgetInput.safeParse({
      accounts, limit: formText(data, 'limit').trim(), warning_percent: Number(data.get('warning_percent')), enabled: data.has('enabled'),
    })
    if (!parsed.success) {
      setError('请填写正数额度、1 至 99 的预警比例，以及 1 至 100 个不同的云账号。')
      return
    }
    const controller = new AbortController()
    writing.current = controller
    setBusy(true)
    setError('')
    try {
      await saveProjectBudget(target, parsed.data, budget?.revision, controller.signal)
      if (!controller.signal.aborted) onSaved()
    } catch (failure) {
      if (!controller.signal.aborted) setError(failure instanceof ConsoleRequestError && [409, 412].includes(failure.status)
        ? '预算已变更，请刷新预算后再修改。' : requestErrorMessage(failure))
    } finally {
      if (writing.current === controller) writing.current = undefined
      if (!controller.signal.aborted) setBusy(false)
    }
  }

  return <form className="catalog-form" onSubmit={(event) => void submit(event)} aria-busy={busy}>
    <fieldset className="detail-form-fields" disabled={busy}>
      <label>预算额度<input name="limit" inputMode="decimal" maxLength={39} required defaultValue={budget?.limit} /></label>
      <label>预警比例（%）<input name="warning_percent" type="number" min={1} max={99} step={1} required
        defaultValue={budget?.warning_percent ?? 80} /></label>
      <label>预算账号（每行一个）<textarea rows={5} name="accounts" required maxLength={12900}
        defaultValue={budget?.accounts.map(({ account }) => account).join('\n')} /></label>
      <label><input name="enabled" type="checkbox" defaultChecked={budget?.enabled ?? true} />启用预算告警</label>
      {error && <p role="alert" className="notice" data-error="true">{error}</p>}
      <button type="submit" className="primary">{budget ? '保存项目预算' : '创建项目预算'}</button>
    </fieldset>
  </form>
}
