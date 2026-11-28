import { z } from 'zod'
import { time } from '../../../shared/api/fields'

const action = z.string().regex(/^(?:[a-z][a-z0-9-]*|\*)$/).max(128)
const actions = z.array(action).max(64).refine((values) =>
  new Set(values).size === values.length && (!values.includes('*') || values.length === 1))
const verifierID = z.string().regex(/^[a-zA-Z0-9][a-zA-Z0-9_.:/-]{0,127}$/)

export const ticketPolicy = z.object({
  revision: z.string().regex(/^[1-9][0-9]*$/),
  actions,
  verifier: verifierID,
  published_by: z.string(),
  published_at: time,
})

export const policyInput = z.object({ actions, verifier: z.union([verifierID, z.literal('')]) })
  .refine((value) => !value.actions.length || value.verifier !== '')

export type TicketPolicy = z.infer<typeof ticketPolicy>
export type PolicyInput = z.infer<typeof policyInput>
