import type { z } from 'zod'
import type { dnsRecordSchema } from './schema'

export type DNSAsset = { id: string; name: string; account: string; ownership_revision: number }
export type DNSRecord = z.infer<typeof dnsRecordSchema>
