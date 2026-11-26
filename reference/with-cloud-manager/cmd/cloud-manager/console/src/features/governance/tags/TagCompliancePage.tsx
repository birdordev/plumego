import { kinds } from '../../../shared/display/labels'
import { TagComplianceReport } from './TagComplianceReport'
import { TagPolicyEditor } from './TagPolicyEditor'
import { useTagPolicy } from './useTagPolicy'

export default function TagCompliancePage({ base, admin }: { base: string; admin: boolean }) {
  const { result, save, reload } = useTagPolicy(base)
  const policy = result.state === 'ready' ? result.policy : undefined
  const canEdit = admin && (result.state === 'ready' || result.state === 'missing')

  return <section aria-labelledby="title" aria-busy={result.state === 'loading'}>
    <div className="row-actions">
      <button type="button" disabled={result.state === 'loading'} onClick={reload}>重新读取规则</button>
    </div>
    {result.state === 'loading' ? <p role="status">正在读取标签规则…</p> : null}
    {result.state === 'missing' ? <p>当前项目没有标签规则。</p> : null}
    {result.state === 'error' ? <p role="alert" className="notice" data-error="true">{result.message}</p> : null}
    {canEdit ? <TagPolicyEditor key={policy?.revision ?? 0} policy={policy} save={save} /> : null}
    {policy ? <>
      <p className="hint">规则版本 {policy.revision}，证据有效期 {policy.max_age_seconds} 秒。</p>
      {policy.rules.length ? <ul>
        {policy.rules.map((rule) => <li key={rule.kind}>{kinds[rule.kind] ?? rule.kind} · {rule.required_keys.join('、')}</li>)}
      </ul> : <p>标签评估已停用。</p>}
      {policy.rules.length ? <TagComplianceReport key={policy.revision} base={base} policy={policy} /> : null}
    </> : null}
  </section>
}
