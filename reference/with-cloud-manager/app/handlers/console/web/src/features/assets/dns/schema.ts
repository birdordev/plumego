import { z } from 'zod'
import { account, time } from '../../../shared/api/fields'

const zoneId = z.string().regex(/^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$/)

export const dnsRecordSchema = z.object({
  id: zoneId, name: z.string().min(1), type: z.string(), status: z.string(),
  ttl: z.number().int().nonnegative(), value_count: z.number().int().nonnegative(),
  line: z.string().optional(), values: z.array(z.string()).optional(),
})

export const dnsPageSchema = z.object({
  observed_at: time, records: z.array(dnsRecordSchema).max(100),
  total: z.number().int().nonnegative(), next_offset: z.number().int().positive().optional(),
})

const dnsRecordTarget = { account, asset_id: zoneId, record_id: zoneId, observed_at: time }

export const dnsDeletionInputSchema = z.strictObject(dnsRecordTarget)
export function dnsUpdateInputSchema(type: string, limit: number) {
  return z.strictObject({
    ...dnsRecordTarget, values: z.array(type === 'A' ? z.ipv4() : z.ipv6()).min(1).max(limit),
    ttl: z.number().int().min(1).max(604800),
  })
}

export const dnsStatusInputSchema = z.strictObject({ ...dnsRecordTarget, enabled: z.boolean() })

const dnsCreationFields = {
  account, asset_id: zoneId, observed_at: time,
  name: z.string().trim().min(1).max(254), ttl: z.number().int().min(1).max(604800),
}
export const dnsCreationInputSchema = z.discriminatedUnion('type', [
  z.strictObject({ ...dnsCreationFields, type: z.literal('A'), value: z.ipv4() }),
  z.strictObject({ ...dnsCreationFields, type: z.literal('AAAA'), value: z.ipv6() }),
])
