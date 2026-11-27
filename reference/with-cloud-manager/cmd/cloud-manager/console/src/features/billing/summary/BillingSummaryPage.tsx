import { useState } from 'react'
import { requestErrorMessage } from '../../../shared/api/requests'
import { useResourceRead } from '../../../shared/api/useResourceRead'
import type { BillingReportLink } from '../report-link'
import { readBillingSummary, validSummaryFilters } from './api'
import { BillingSummaryFilters } from './BillingSummaryFilters'
import { BillingSummaryResults } from './BillingSummaryResults'

type Props = { base: string; filters: string; commitFilters: (input: string) => string; billLink: BillingReportLink }

export default function BillingSummaryPage(props: Props) {
  return <BillingSummary key={JSON.stringify([props.base, props.filters])} {...props} />
}

function BillingSummary({ base, filters, commitFilters, billLink }: Props) {
  const [cursors, setCursors] = useState([''])
  const [attempt, setAttempt] = useState(0)
  const [invalid, setInvalid] = useState(false)
  const params = new URLSearchParams(filters)
  const valid = validSummaryFilters(params)
  const after = cursors[cursors.length - 1]
  if (after) params.set('after', after)
  const target = base + '/billing/summary?' + params.toString()

  function refresh() {
    setCursors([''])
    setAttempt((value) => value + 1)
  }
  function apply(input: string) {
    if (input !== '' && !validSummaryFilters(new URLSearchParams(input))) {
      setInvalid(true)
      return
    }
    setInvalid(false)
    if (commitFilters(input) === filters) refresh()
  }
  return <div className="collection-page">
    <BillingSummaryFilters filters={filters} apply={apply} />
    {(invalid || (filters && !valid)) && <p role="alert">请核对原币种与账期。最多查询连续 12 个月。</p>}
    {valid && <>
      <div className="row-actions"><button type="button" onClick={refresh}>刷新汇总</button></div>
      <SummaryPage key={target + '#' + attempt} target={target} billLink={billLink}
        previous={after ? () => setCursors((current) => current.slice(0, -1)) : undefined}
        advance={(next) => setCursors((current) => [...current, next])} />
    </>}
  </div>
}

function SummaryPage({ target, billLink, previous, advance }: {
  target: string; billLink: BillingReportLink; previous?: () => void; advance: (next: string) => void
}) {
  const result = useResourceRead(target, readBillingSummary)
  if (result.state === 'loading') return <p role="status">正在读取费用汇总…</p>
  const next = result.state === 'ready' ? result.data.next : undefined
  return <>
    {result.state === 'ready'
      ? <BillingSummaryResults summary={result.data} billLink={billLink} />
      : <p role="alert">{requestErrorMessage(result.error)}</p>}
    {(previous || next) && <div className="row-actions">
      {previous && <button type="button" onClick={previous}>上一页</button>}
      {next && <button type="button" onClick={() => advance(next)}>下一页</button>}
    </div>}
  </>
}
