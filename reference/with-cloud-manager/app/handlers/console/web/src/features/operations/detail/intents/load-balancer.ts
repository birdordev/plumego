import { z } from 'zod'

const text = z.string()
const integer = z.number().int()
const group = { target_group_id: text, ownership_revision: integer }
const member = { ...group, member_key: text }

export const loadBalancerIntents = {
  load_balancer_backend_attach_intent: z.object({
    ...group, instance_id: text, instance_revision: integer, port: integer, member_key: text.optional(),
  }).optional(),
  load_balancer_backend_detach_intent: z.object(member).optional(),
  load_balancer_backend_weight_intent: z.object({ ...member, current_weight: integer, weight: integer }).optional(),
  load_balancer_rename_intent: z.object({
    ownership_revision: integer, product: text, current_name: text, name: text, listener_id: text.optional(), target_group_id: text.optional(),
  }).optional(),
}
