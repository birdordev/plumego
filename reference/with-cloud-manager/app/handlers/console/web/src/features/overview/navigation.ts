import type { WorkspaceLink as Link } from '../../shared/navigation/workspace-link'

type OverviewDestination = 'assets' | 'inventory-freshness' | 'operations' | 'alerts'
export type WorkspaceLink = Link<OverviewDestination>
