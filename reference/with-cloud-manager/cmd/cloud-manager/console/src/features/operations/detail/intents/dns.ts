import { z } from 'zod'
import { dnsRecordSchema } from '../../../assets/dns/schema'

const text = z.string()
const integer = z.number().int()
const flag = z.boolean()

export const dnsIntents = {
  dns_record_creation_intent: z.object({
    zone_name: text, name: text, type: text, value: text, ttl: integer, record_id: text.optional(), needs_record_id: flag.optional(),
  }).optional(),
  dns_record_deletion_intent: z.object({ zone_name: text, record: dnsRecordSchema }).optional(),
  dns_record_update_intent: z.object({
    zone_name: text, record: dnsRecordSchema, target_values: z.array(text).min(1).max(128), target_ttl: integer,
  }).optional(),
  dns_record_status_intent: z.object({ zone_name: text, name: text, record_id: text, current_status: text, enabled: flag }).optional(),
}
