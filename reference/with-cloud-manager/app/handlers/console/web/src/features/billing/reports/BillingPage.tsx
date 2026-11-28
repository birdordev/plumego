import { useCallback, useState, type FormEvent } from 'react'
import { ConsoleRequestError, requestErrorMessage } from '../../../shared/api/requests'
import { useResourceRead } from '../../../shared/api/useResourceRead'
import { formText } from '../../../shared/detail/form-values'
import { BillingReportView } from './BillingReportView'
import { billingQuerySchema, type BillingQuery } from '../schema'
import { readBillingReport } from './api'

type Props = { base: string; filters: string; commitFilters: (input: string) => string }

export default function BillingPage(props: Props) {
  return <AccountBillingPage key={JSON.stringify([props.base, props.filters])} {...props} />
}

function AccountBillingPage({ base, filters, commitFilters }: Props) {
  const [invalid, setInvalid] = useState(false)
  const [attempt, setAttempt] = useState(0)
  const values = new URLSearchParams(filters)
  const selected = billingQuerySchema.safeParse(Object.fromEntries(values))
  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    const data = new FormData(event.currentTarget)
    const parsed = billingQuerySchema.safeParse({
      account: formText(data, 'account'),
      period: formText(data, 'period'),
      source: formText(data, 'source'),
      revision: formText(data, 'revision') || undefined,
    })
    if (!parsed.success) {
      setInvalid(true)
      return
    }
    setInvalid(false)
    const { account, period, source, revision } = parsed.data
    const next = new URLSearchParams({ account, period, source })
    if (revision) next.set('revision', revision)
    if (commitFilters(next.toString()) === filters) setAttempt((value) => value + 1)
  }

  return (
    <section aria-labelledby="title">
      <form className="filters" onSubmit={submit}>
        <label>云账号<input name="account" required maxLength={128} defaultValue={values.get('account') ?? ''} /></label>
        <label>账期<input name="period" type="month" required placeholder="YYYY-MM" defaultValue={values.get('period') ?? ''} /></label>
        <label>账单来源<select name="source" defaultValue={values.get('source') ?? 'native'}>
          <option value="native">厂商账单</option>
          <option value="focus-1.0">FOCUS 1.0</option>
        </select></label>
        <label>版本<input name="revision" inputMode="numeric" pattern="[1-9][0-9]{0,18}" maxLength={19} placeholder="当前版本"
          defaultValue={values.get('revision') ?? ''} /></label>
        <button type="submit" className="primary">查询账单</button>
      </form>
      {(invalid || (filters && !selected.success)) && <p className="notice" data-error="true" role="alert">请核对账号、账期和版本。</p>}
      {!invalid && selected.success && <BillingResult key={attempt} base={base} query={selected.data} />}
    </section>
  )
}

function BillingResult({ base, query }: { base: string; query: BillingQuery }) {
  const { account, period, source, revision } = query
  const read = useCallback((path: string, signal: AbortSignal) => readBillingReport(path, { account, period, source, revision }, signal),
    [account, period, source, revision])
  const result = useResourceRead(base, read)
  switch (result.state) {
    case 'loading': return <p role="status">正在读取账单…</p>
    case 'ready': return <BillingReportView report={result.data} />
    case 'error': return <p className="notice" data-error="true" role="alert">{result.error instanceof ConsoleRequestError && result.error.status === 404
      ? '当前条件下没有已发布的账单版本。' : requestErrorMessage(result.error)}</p>
  }
}
