import { useCallback, useEffect } from 'react'
import { readPage as readAlerts } from '../alerts/api'
import { readInventoryFreshness } from '../governance/freshness/api'
import { readPage as readOperations } from '../operations/history/api'
import { ConsoleRequestError } from '../../shared/api/requests'
import { useResourceRead } from '../../shared/api/useResourceRead'

const recentLimit = 5

export function useOverview(base: string, admin: boolean, expireSession: () => void) {
  const listOperations = useCallback((target: string, signal: AbortSignal) =>
    readOperations({ base: target, admin, filters: '', after: '', limit: recentLimit, signal }), [admin])
  const listAlerts = useCallback((target: string, signal: AbortSignal) =>
    readAlerts({ base: target, admin, filters: 'status=active', after: '', limit: recentLimit, signal }), [admin])

  const freshness = useResourceRead(base + '/governance/inventory-freshness?limit=1', readInventoryFreshness)
  const operations = useResourceRead(base, listOperations)
  const alerts = useResourceRead(base, listAlerts)
  const unauthorized = [freshness, operations, alerts].some((result) =>
    result.state === 'error' && result.error instanceof ConsoleRequestError && result.error.status === 401)

  useEffect(() => {
    if (unauthorized) expireSession()
  }, [unauthorized, expireSession])

  return { freshness, operations, alerts }
}

type Result = ReturnType<typeof useOverview>
export type OverviewFreshness = Result['freshness']
export type OverviewOperations = Result['operations']
export type OverviewAlerts = Result['alerts']
