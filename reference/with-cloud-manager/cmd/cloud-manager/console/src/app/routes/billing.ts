import { isAdmin, projectProps } from './project-context'
import { projectPage, type ProjectPageLink } from './project-page'

function reportLink(link: ProjectPageLink) {
  return (params: { account: string; period: string; source: string; revision: string }) =>
    link('billing', new URLSearchParams(params).toString())
}

export const billingRoutes = {
  'billing-summary': projectPage('费用汇总', () => import('../../features/billing/summary/BillingSummaryPage'),
    (context) => ({
      ...projectProps(context), filters: context.filters, commitFilters: context.commitFilters,
      billLink: reportLink(context.link),
    }), { allowed: isAdmin }),
  billing: projectPage('云账单', () => import('../../features/billing/reports/BillingPage'),
    (context) => ({ ...projectProps(context), filters: context.filters, commitFilters: context.commitFilters }), { allowed: isAdmin }),
  'billing-budget': projectPage('云预算', () => import('../../features/billing/budgets/BudgetsPage'), projectProps, { allowed: isAdmin }),
  'project-budget': projectPage('项目预算', () => import('../../features/billing/project-budget/ProjectBudgetPage'),
    (context) => ({
      ...projectProps(context), filters: context.filters, commitFilters: context.commitFilters, billLink: reportLink(context.link),
    }), { allowed: isAdmin }),
  'billing-allocation': projectPage('费用分摊', () => import('../../features/billing/allocation/AllocationPage'),
    projectProps, { allowed: isAdmin }),
}
