import type { FormEvent } from 'react'
import { kinds } from '../../assets/labels'
import { TagComplianceTable } from './TagComplianceTable'
import type { TagPolicy } from './schema'
import { useTagReport } from './useTagReport'

export function TagComplianceReport({ base, policy }: { base: string; policy: TagPolicy }) {
  const { report, loading, error, search, more } = useTagReport(base, policy.revision)

  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    const query = new URLSearchParams({ limit: '30' })
    for (const [name, value] of new FormData(event.currentTarget)) {
      if (typeof value === 'string' && value.trim()) query.set(name, value.trim())
    }
    search(query)
  }

  return <section aria-label="标签评估结果" aria-busy={loading}>
    <form className="filters" onSubmit={submit}>
      <label>资源类型<select name="kind">
        {policy.rules.map((rule) => <option key={rule.kind} value={rule.kind}>{kinds[rule.kind] ?? rule.kind}</option>)}
      </select></label>
      <label>合规状态<select name="state">
        <option value="">全部</option><option value="compliant">合规</option>
        <option value="noncompliant">缺少必填标签</option><option value="unknown">证据不足</option>
      </select></label>
      <label>账号<input name="account" maxLength={256} /></label>
      <label>地域<input name="region" maxLength={256} /></label>
      <button className="primary" type="submit">查询合规</button>
    </form>
    {loading ? <p role="status">正在评估标签…</p> : null}
    {error ? <p className="notice" role="alert" data-error="true">{error}</p> : null}
    {report ? <TagComplianceTable items={report.items} /> : null}
    {report?.next ? <button type="button" onClick={more} disabled={loading}>加载下一页</button> : null}
  </section>
}
