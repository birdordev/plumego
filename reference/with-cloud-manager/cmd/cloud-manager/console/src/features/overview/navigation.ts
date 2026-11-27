import type { ComponentProps } from 'react'

type OverviewDestination = 'assets' | 'inventory-freshness' | 'operations' | 'alerts'
export type WorkspaceLink = (view: OverviewDestination, filters?: string) => Pick<ComponentProps<'a'>, 'href' | 'onClick'>
