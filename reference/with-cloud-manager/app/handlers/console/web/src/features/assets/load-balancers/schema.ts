import { z } from 'zod'
import { time } from '../../../shared/api/fields'

const listenerSchema = z.object({
  id: z.string(),
  name: z.string().optional(),
  protocol: z.string(),
  port: z.number().int(),
  end_port: z.number().int().optional(),
  target_group_ids: z.array(z.string()).optional(),
})

const targetGroupSchema = z.object({
  id: z.string(),
  name: z.string().optional(),
  kind: z.string().optional(),
  protocol: z.string().optional(),
  algorithm: z.string().optional(),
  exclusive: z.boolean().optional(),
})

const backendSchema = z.object({
  key: z.string(),
  group_id: z.string(),
  port: z.number().int(),
  weight: z.number().int().optional(),
  role: z.string().optional(),
  weight_editable: z.boolean().optional(),
})

const healthSchema = z.object({
  key: z.string(),
  listener_id: z.string(),
  group_id: z.string().optional(),
  rule_id: z.string().optional(),
  port: z.number().int(),
  listener_port: z.number().int().optional(),
  status: z.string(),
})

export const configurationSchema = z.object({
  observed_at: time,
  listeners: z.array(listenerSchema).nullable().transform((items) => items ?? []),
  target_groups: z.array(targetGroupSchema).nullable().transform((items) => items ?? []),
  backends: z.object({
    members: z.array(backendSchema).nullable().transform((items) => items ?? []),
    checks: z.array(healthSchema).nullable().transform((items) => items ?? []),
  }).optional(),
})

export type BackendMember = z.infer<typeof backendSchema>
export type TargetGroup = z.infer<typeof targetGroupSchema>
