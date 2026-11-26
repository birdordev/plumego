import { ConsoleRequestError } from '../../../shared/api/requests'
import { request } from '../../session/client'
import { ticketPolicy, type PolicyInput } from './schema'

export async function readTicketPolicy(target: string, signal: AbortSignal) {
  try {
    return await request(target, { signal }, ticketPolicy)
  } catch (error) {
    if (error instanceof ConsoleRequestError && error.status === 404) return undefined
    throw error
  }
}

export function saveTicketPolicy(target: string, input: PolicyInput, revision: string | undefined, signal: AbortSignal) {
  return request(target, { method: revision ? 'PUT' : 'POST', match: revision, body: JSON.stringify(input), signal }, ticketPolicy)
}
