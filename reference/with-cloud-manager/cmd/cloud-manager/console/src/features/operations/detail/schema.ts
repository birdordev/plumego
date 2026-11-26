import { z } from 'zod'
import { time } from '../../../shared/api/fields'
import { operationSchema } from '../schema'
import { computeIntents } from './intents/compute'
import { renameIntents } from './intents/asset-rename'
import { dnsIntents } from './intents/dns'
import { loadBalancerIntents } from './intents/load-balancer'
import { networkIntents } from './intents/network'

export const operationDetailSchema = operationSchema.extend({
  account: z.string(),
  region: z.string(),
  provider_request_id: z.string().optional(),
  catalog_template: z.string().optional(),
  catalog_version: z.number().int().optional(),
  ticket_policy_revision: z.string().regex(/^[1-9][0-9]*$/).optional(),
  ...computeIntents,
  ...renameIntents,
  ...dnsIntents,
  ...loadBalancerIntents,
  ...networkIntents,
})

export type OperationDetail = z.infer<typeof operationDetailSchema>

export const operationEventsSchema = z.object({
  events: z.array(z.object({ recorded_at: time, status: z.string() })).max(100),
  next_after: z.string().regex(/^\d+$/),
  has_more: z.boolean(),
})

export type OperationEvents = z.infer<typeof operationEventsSchema>
