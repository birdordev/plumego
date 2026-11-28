import { request } from '../../../session/client'
import { operationEventsSchema } from './schema'

export function readOperationEvents(target: string, signal: AbortSignal, after?: string) {
  const query = after === undefined ? '' : '?' + new URLSearchParams({ after })
  return request(target + '/events' + query, { signal }, operationEventsSchema)
}
