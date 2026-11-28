import { ConsoleTable } from '../../../shared/components/ConsoleTable'
import { DetailFields } from '../../../shared/components/DetailFields'
import type { BillingReportLink } from '../report-link'
import type { ProjectBudget } from './api'

const budgetStatus = {
  inactive: '已停用', unavailable: '等待完整费用', normal: '低于预警线', warning: '达到预警线', exceeded: '达到预算额度',
}
const accountStatus = {
  account_unavailable: '账号当前不可读', report_missing: '待导入', amount_missing: '缺少所选费用口径', observed: '已计入',
}

export function ProjectBudgetDetails({ budget, billLink }: { budget: ProjectBudget; billLink: BillingReportLink }) {
  return <section aria-labelledby="project-budget-heading">
    <h2 id="project-budget-heading">{budget.period} · {budget.currency}</h2>
    <DetailFields fields={[
      ['预算状态', budgetStatus[budget.state]],
      ['累计费用', budget.spent],
      ['已计入账号', budget.observed_accounts + ' / ' + budget.accounts.length],
      ['预算版本', budget.revision],
    ]} />
    <ConsoleTable columns={['云账号', '账单状态', '账单版本', '费用']} rows={budget.accounts.map((item) => ({
      key: item.account, cells: [item.account, accountStatus[item.state], item.report_revision
        ? <a {...billLink({ account: item.account, period: budget.period, source: budget.source, revision: item.report_revision })}>
          查看版本 {item.report_revision}
        </a> : undefined, item.amount],
    }))} />
  </section>
}
