import { useState, type FormEvent } from 'react'
import { ConsoleRequestError, requestErrorMessage } from '../../../shared/api/requests'
import { tagKinds, tagPolicyInput, type TagPolicy, type TagPolicyInput } from './schema'
import { TagPolicyRuleFields, type RuleDraft } from './TagPolicyRuleFields'

type Props = { policy?: TagPolicy; save: (input: TagPolicyInput) => Promise<void> }

function asDraft(kind: string, keys = ''): RuleDraft {
  return { id: crypto.randomUUID(), kind, keys }
}

export function TagPolicyEditor({ policy, save }: Props) {
  const [maxAge, setMaxAge] = useState(String(policy?.max_age_seconds ?? 3600))
  const [notify, setNotify] = useState(policy?.alert_on_noncompliance ?? false)
  const [rules, setRules] = useState(() => policy?.rules.map((rule) => asDraft(rule.kind, rule.required_keys.join('\n'))) ?? [])
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const availableKind = tagKinds.options.find((kind) => rules.every((rule) => rule.kind !== kind))

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (busy) return
    const input = tagPolicyInput.safeParse({
      max_age_seconds: Number(maxAge),
      alert_on_noncompliance: notify,
      rules: rules.map(({ kind, keys }) => ({ kind, required_keys: keys.split('\n').map((key) => key.trim()).filter(Boolean) })),
    })
    if (!input.success) {
      setError('请核对有效期、资源类型及标签键。类型和键各自只能填写一次。')
      return
    }
    setBusy(true)
    setError('')
    try {
      await save(input.data)
    } catch (error) {
      setError(error instanceof ConsoleRequestError && (error.status === 409 || error.status === 412)
        ? '标签规则已更新。请重新读取规则后再发布。' : requestErrorMessage(error))
    } finally {
      setBusy(false)
    }
  }

  return <details>
    <summary>{policy ? '编辑标签规则' : '创建标签规则'}</summary>
    <form onSubmit={(event) => void submit(event)} className="filters" aria-busy={busy}>
      <label>证据有效期（秒）
        <input type="number" value={maxAge} min={60} max={604800} required disabled={busy}
          onChange={(event) => setMaxAge(event.target.value)} />
      </label>
      <label><input type="checkbox" checked={notify} disabled={busy}
        onChange={(event) => setNotify(event.target.checked)} />缺少必填标签时生成项目告警</label>
      {rules.map((draft, index) => <TagPolicyRuleFields key={draft.id} index={index} draft={draft} disabled={busy}
        onChange={(next) => setRules((current) => current.map((rule) => rule.id === next.id ? next : rule))}
        onRemove={() => setRules((current) => current.filter((rule) => rule.id !== draft.id))} />)}
      <div className="row-actions">
        <button type="button" disabled={busy || !availableKind}
          onClick={() => availableKind && setRules((current) => [...current, asDraft(availableKind)])}>添加规则</button>
        <button type="submit" className="primary" disabled={busy}>发布规则</button>
      </div>
      {rules.length === 0 ? <p className="hint">发布空规则会停止当前项目的标签合规评估。</p> : null}
      {error ? <p role="alert" className="notice" data-error="true">{error}</p> : null}
    </form>
  </details>
}
