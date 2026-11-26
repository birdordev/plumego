import { z } from 'zod'
import { time } from '../../../shared/api/fields'
import { request } from '../../session/client'

const revision = z.string().regex(/^[1-9][0-9]*$/)
const changeTicket = z.object({
  policy_revision: revision,
  ready: z.boolean(),
  evidence: z.object({
    ticket_id: z.string(),
    source_revision: revision,
    decision: z.enum(['approved', 'revoked']),
    valid_until: time.optional(),
    recorded_at: time,
  }).optional(),
})

export function readChangeTicket(target: string, signal: AbortSignal) {
  return request(target, { signal }, changeTicket)
}
