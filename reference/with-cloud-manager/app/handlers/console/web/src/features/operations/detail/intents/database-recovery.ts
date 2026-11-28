import { z } from 'zod'
import { time } from '../../../../shared/api/fields'

export const databaseRecoveryIntent = z.object({
  source_id: z.string(), name: z.string(), restore_time: time, observed_at: time,
  vpc_id: z.string(), subnet_id: z.string(), security_group_id: z.string(), zone: z.string(),
  flavor: z.string(), storage_type: z.string(), storage_gib: z.number().int(), target_id: z.string().optional(),
  cpu: z.number().int().positive().optional(), memory_mib: z.number().int().positive().optional(),
  can_retry_observation: z.boolean().optional(),
})
