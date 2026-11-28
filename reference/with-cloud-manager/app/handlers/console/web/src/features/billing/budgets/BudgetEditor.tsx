import { useEffect, useRef, useState, type FormEvent } from 'react'
import { ConsoleRequestError, requestErrorMessage } from '../../../shared/api/requests'
import { DetailFields } from '../../../shared/components/DetailFields'
import { formText } from '../../../shared/detail/form-values'
import { writeBudget } from './api'
import { budgetInputSchema, type Budget, type BudgetKey } from './schema'

const states = {
  inactive: '已停用', unavailable: '费用口径尚不可用', normal: '低于预警线', warning: '达到预警线', exceeded: '达到预算额度',
}

type Props = { base: string; scope: BudgetKey; budget?: Budget; onSaved: (budget: Budget) => void }

export function BudgetEditor({ base, scope, budget, onSaved }: Props) {
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const mounted = useRef(false)
  const writing = useRef(false)
  useEffect(() => {
    mounted.current = true
    return () => { mounted.current = false }
  }, [])

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (writing.current) return
    const data = new FormData(event.currentTarget)
    const input = budgetInputSchema.safeParse({
      limit: formText(data, 'limit').trim(), warning_percent: Number(data.get('warning_percent')), enabled: data.has('enabled'),
    })
    if (!input.success) {
      setError('额度必须为正数，预警比例为 1 至 99。')
      return
    }
    writing.current = true
    setBusy(true)
    setError('')
    try {
      const saved = await writeBudget(base, scope, input.data, budget?.revision)
      if (mounted.current) onSaved(saved)
    } catch (failure) {
      if (mounted.current) setError(failure instanceof ConsoleRequestError && [409, 412].includes(failure.status)
        ? '预算已变更，请重新查询后再修改。' : requestErrorMessage(failure))
    } finally {
      writing.current = false
      if (mounted.current) setBusy(false)
    }
  }

  return <>
    <h2>{scope.account} · {scope.period} · {scope.currency}</h2>
    <DetailFields fields={[
      ['来源', scope.source === 'native' ? '厂商账单' : 'FOCUS 1.0'],
      ['成本口径', scope.basis === 'billed' ? '账单金额' : '摊销成本'],
      ['预算状态', budget ? states[budget.state] : '尚未设置'],
      ['累计费用', budget?.spent],
      ['账单版本', budget?.report_revision],
      ['预算版本', budget?.revision],
    ]} />
    <form className="catalog-form" onSubmit={(event) => void submit(event)} aria-busy={busy}>
      <fieldset className="detail-form-fields" disabled={busy}>
        <label>预算额度<input name="limit" required inputMode="decimal" maxLength={39} defaultValue={budget?.limit} /></label>
        <label>预警比例（%）<input name="warning_percent" type="number" min={1} max={99} step={1} required
          defaultValue={budget?.warning_percent ?? 80} /></label>
        <label><input name="enabled" type="checkbox" defaultChecked={budget?.enabled ?? true} />启用预算告警</label>
        {error ? <p role="alert" className="notice" data-error="true">{error}</p> : null}
        <button type="submit" className="primary">{budget ? '保存预算' : '创建预算'}</button>
      </fieldset>
    </form>
  </>
}
