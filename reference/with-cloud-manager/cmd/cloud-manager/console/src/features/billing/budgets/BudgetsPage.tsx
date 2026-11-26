import type { FormEvent } from 'react'
import { formText } from '../../../shared/detail/form-values'
import { BudgetEditor } from './BudgetEditor'
import { budgetKeySchema } from './schema'
import { useBudgetLookup } from './useBudgetLookup'

export default function BudgetsPage({ base }: { base: string }) {
  const { result, load, showError, apply } = useBudgetLookup(base)

  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    const data = new FormData(event.currentTarget)
    const key = budgetKeySchema.safeParse({
      account: formText(data, 'account'), period: formText(data, 'period'), source: formText(data, 'source'),
      currency: formText(data, 'currency').trim().toUpperCase(), basis: formText(data, 'basis'),
    })
    if (key.success) void load(key.data)
    else showError('请核对账号、账期和三位原币种代码。')
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
        <label>原币种<input name="currency" required maxLength={3} placeholder="CNY" /></label>
        <label>成本口径<select name="basis">
          <option value="billed">账单金额</option>
          <option value="effective">摊销成本</option>
        </select></label>
        <button className="primary" type="submit">查询预算</button>
      </form>
      {result.state === 'loading' ? <p role="status">正在读取预算…</p> : null}
      {result.state === 'error' ? <p role="alert" className="notice" data-error="true">{result.message}</p> : null}
      {result.state === 'ready' ? <BudgetEditor key={JSON.stringify([result.key, result.budget?.revision])} base={base}
        scope={result.key} budget={result.budget} onSaved={(budget) => apply(result.key, budget)} /> : null}
    </section>
  )
}
