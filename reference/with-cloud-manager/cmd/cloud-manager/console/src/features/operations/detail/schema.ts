import { z } from 'zod'
import { operationSchema } from '../schema'
import { deploymentViewSchema } from './bare-metal-deployment/schema'
import { computeIntents } from './intents/compute'
import { renameIntents } from './intents/asset-rename'
import { databaseRecoveryIntent } from './intents/database-recovery'
import { dnsIntents } from './intents/dns'
import { loadBalancerIntents } from './intents/load-balancer'
import { networkIntents } from './intents/network'

export const operationDetailSchema = operationSchema.extend({
  account: z.string(),
  region: z.string(),
  provider_request_id: z.string().optional(),
  can_retry_power_observation: z.boolean().optional(),
  catalog_template: z.string().optional(),
  catalog_version: z.number().int().optional(),
  ticket_policy_revision: z.string().regex(/^[1-9][0-9]*$/).optional(),
  bucket_encryption_intent: z.object({
    location: z.string(),
    current_state: z.literal('unconfigured'),
    target_state: z.literal('aes256'),
  }).optional(),
  bucket_versioning_intent: z.object({
    location: z.string(),
    current_state: z.enum(['disabled', 'enabled', 'suspended']),
    target_state: z.enum(['enabled', 'suspended']),
  }).optional(),
  bare_metal_deployment: deploymentViewSchema.optional(),
  database_recovery_intent: databaseRecoveryIntent.optional(),
  ...computeIntents,
  ...renameIntents,
  ...dnsIntents,
  ...loadBalancerIntents,
  ...networkIntents,
})

export type OperationDetail = z.infer<typeof operationDetailSchema>
