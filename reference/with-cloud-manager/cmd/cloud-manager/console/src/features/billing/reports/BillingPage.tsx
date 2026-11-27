import type { FormEvent } from 'react'
import { formText } from '../../../shared/detail/form-values'
import { BillingReportView } from './BillingReportView'
import { billingQuerySchema } from '../schema'
import { useBillingReport } from './useBillingReport'

export default function BillingPage({ base }: { base: string }) {
  const { result, load, invalidQuery } = useBillingReport(base)

  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    const data = new FormData(event.currentTarget)
    const parsed = billingQuerySchema.safeParse({
      account: formText(data, 'account'),
      period: formText(data, 'period'),
      source: formText(data, 'source'),
      revision: formText(data, 'revision') || undefined,
    })
    if (parsed.success) void load(parsed.data)
    else invalidQuery()
  }

  return (
    <section aria-labelledby="title" aria-busy={result.state === 'loading'}>
      <form className="filters" onSubmit={submit}>
        <label>云账号<input name="account" required maxLength={128} /></label>
        <label>账期<input name="period" type="month" required placeholder="YYYY-MM" /></label>
        <label>账单来源<select name="source">
          <option value="native">厂商账单</option>
          <option value="focus-1.0">FOCUS 1.0</option>
        </select></label>
        <label>版本<input name="revision" inputMode="numeric" pattern="[1-9][0-9]{0,18}" maxLength={19} placeholder="当前版本" /></label>
        <button type="submit" className="primary">查询账单</button>
      </form>
      {result.state === 'loading' ? <p role="status">正在读取账单…</p> : null}
      {result.state === 'error' ? <p className="notice" data-error="true" role="alert">{result.message}</p> : null}
      {result.state === 'ready' ? <BillingReportView report={result.report} /> : null}
    </section>
  )
}
