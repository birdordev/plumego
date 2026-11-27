import { isAdmin, projectProps } from './project-context'
import { projectPage } from './project-page'

export const billingRoutes = {
  billing: projectPage('云账单', () => import('../../features/billing/reports/BillingPage'), projectProps, { allowed: isAdmin }),
  'billing-budget': projectPage('云预算', () => import('../../features/billing/budgets/BudgetsPage'), projectProps, { allowed: isAdmin }),
  'billing-allocation': projectPage('费用分摊', () => import('../../features/billing/allocation/AllocationPage'),
    projectProps, { allowed: isAdmin }),
}
