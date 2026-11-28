import { z } from 'zod'
import { time } from '../../../../shared/api/fields'

const operationID = z.string().regex(/^[a-zA-Z0-9][a-zA-Z0-9_.:/-]{0,127}$/)
const digest = z.string().regex(/^[0-9a-f]{64}$/)

export const registrationSchema = z.object({
  stage_operation: operationID,
  boot_operation: operationID,
  installer_id: operationID,
  image_sha256: digest,
})

export const deploymentSchema = registrationSchema.extend({
  id: operationID,
  account: z.string().min(1),
  site: z.string().min(1),
  system_id: z.string().regex(/^[0-9a-f]{8}(?:-[0-9a-f]{4}){3}-[0-9a-f]{12}$/),
  state: z.enum(['pending', 'installed', 'failed', 'boot_failed', 'expired']),
  evidence_sha256: digest.optional(),
  created_at: time,
  expires_at: time,
  recorded_at: time.optional(),
})

export const deploymentViewSchema = z.union([
  z.object({ deployment: deploymentSchema, stage_operation: z.never().optional() }),
  z.object({ stage_operation: operationID, deployment: z.never().optional() }),
])

export type Registration = z.infer<typeof registrationSchema>
export type Deployment = z.infer<typeof deploymentSchema>
export type DeploymentView = z.infer<typeof deploymentViewSchema>
