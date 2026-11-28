import type { ComponentProps } from 'react'

export type BillingReportLink = (query: { account: string; period: string; source: string; revision: string }) =>
  Pick<ComponentProps<'a'>, 'href' | 'onClick'>
