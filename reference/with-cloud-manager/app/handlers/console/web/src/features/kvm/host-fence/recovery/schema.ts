import { z } from 'zod'
import { revision, time } from '../../../../shared/api/fields'
import { evidenceSchema, siteID } from '../../schema'

export const observationSchema = z.enum(['defined', 'running', 'absent'])
export type Observation = z.infer<typeof observationSchema>

export const recoverySchema = z.object({
  recovery_id: siteID, vm_id: z.guid(), host: siteID, host_uuid: z.guid(), fence_generation: revision,
  vm_revision: revision, source_state: z.string().min(1), observed: observationSchema,
  evidence_sha256: evidenceSchema, second_evidence_sha256: evidenceSchema.optional(),
  status: z.enum(['pending', 'approved', 'rejected']), result_state: z.string().optional(),
  requester: z.string().min(1), reviewer: z.string().min(1).optional(), requested_at: time, reviewed_at: time.optional(),
})
export const recoveryView = z.object({ recovery: recoverySchema.nullable() })
export type Recovery = z.infer<typeof recoverySchema>

export const observationLabels = { defined: '持久域存在，处于关机状态', running: '持久域正在运行', absent: '确认域缺失' }

export function observationsFor(state: string): readonly Observation[] {
  switch (state) {
    case 'staging': case 'staged': return ['absent']
    case 'defining': case 'defined': case 'undefining': return ['defined', 'absent']
    case 'starting': return ['running', 'defined', 'absent']
    case 'stopping': return ['defined', 'running']
    default: return []
  }
}
