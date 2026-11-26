import { z } from 'zod'

const text = z.string()
const integer = z.number().int()
const flag = z.boolean()

export const networkIntents = {
  network_deletion_intent: z.object({
    ownership_revision: integer, name: text, parent_id: text.optional(), parent_revision: integer.optional(), rule_removal_confirmed: flag.optional(),
  }).optional(),
  network_intent: z.object({
    name: text, parent_revision: integer, parent_id: text.optional(), cidr: text.optional(), zone: text.optional(),
    bandwidth_mbps: integer.optional(), address_pool: text.optional(), asset_revision: integer.optional(),
    public_address: text.optional(), port_id: text.optional(),
    rule: z.object({ direction: text, protocol: text, from_port: integer, to_port: integer, remote_cidr: text }).optional(),
  }).optional(),
}
