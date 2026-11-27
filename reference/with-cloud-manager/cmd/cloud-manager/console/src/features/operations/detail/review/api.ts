import { request } from '../../../session/client'
import { changeTicketSchema } from './schema'

export function readChangeTicket(target: string, signal: AbortSignal) {
  return request(target, { signal }, changeTicketSchema)
}
