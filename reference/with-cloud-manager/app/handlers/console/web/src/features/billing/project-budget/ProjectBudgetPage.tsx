import { useState } from 'react'
import { requestErrorMessage } from '../../../shared/api/requests'
import { useResourceRead } from '../../../shared/api/useResourceRead'
import { CollectionFilters } from '../../../shared/collection/CollectionFilters'
import { SelectField } from '../../../shared/components/SelectField'
import { ProjectBudgetDetails } from './ProjectBudgetDetails'
import { ProjectBudgetEditor } from './ProjectBudgetEditor'
import { projectBudgetKey, readProjectBudget } from './api'
import type { BillingReportLink } from '../report-link'

type Props = { base: string; filters: string; commitFilters: (input: string) => string; billLink: BillingReportLink }

export default function ProjectBudgetPage(props: Props) {
  return <ProjectBudgetScope key={JSON.stringify([props.base, props.filters])} {...props} />
}

function ProjectBudgetScope({ base, filters, commitFilters, billLink }: Props) {
  const [attempt, setAttempt] = useState(0)
  const valid = projectBudgetKey.safeParse(Object.fromEntries(new URLSearchParams(filters))).success
  const refresh = () => setAttempt((current) => current + 1)
  return <div className="collection-page">
    <CollectionFilters values={filters} onApply={(input) => { if (commitFilters(input) === filters) refresh() }}>{(values) => <>
      <label>账期<input name="period" type="month" min="2000-01" required defaultValue={values.get('period') ?? ''} /></label>
      <SelectField label="账单来源" name="source" initialValue={values.get('source')} options={[[ 'native', '厂商账单' ], ['focus-1.0', 'FOCUS 1.0']]} />
      <label>原币种<input name="currency" required pattern="[A-Z]{3}" maxLength={3} defaultValue={values.get('currency') ?? 'CNY'} /></label>
      <SelectField label="成本口径" name="basis" initialValue={values.get('basis')} options={[[ 'billed', '账单金额' ], ['effective', '摊销成本']]} />
    </>}</CollectionFilters>
    {filters && !valid && <p role="alert">请核对预算账期、原币种与费用口径。</p>}
    {valid && <>
      <div className="row-actions"><button type="button" onClick={refresh}>刷新预算</button></div>
      <ProjectBudgetResult key={attempt} target={base + '/billing/project-budget?' + filters} onSaved={refresh} billLink={billLink} />
    </>}
  </div>
}

function ProjectBudgetResult({ target, onSaved, billLink }: { target: string; onSaved: () => void; billLink: BillingReportLink }) {
  const result = useResourceRead(target, readProjectBudget)
  if (result.state === 'loading') return <p role="status">正在读取项目预算…</p>
  if (result.state === 'error') return <p role="alert">{requestErrorMessage(result.error)}</p>
  return <>
    {result.data && <ProjectBudgetDetails budget={result.data} billLink={billLink} />}
    <ProjectBudgetEditor target={target} budget={result.data} onSaved={onSaved} />
  </>
}
