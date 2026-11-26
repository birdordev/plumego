import { z } from 'zod'
import { pageSchema } from '../../../shared/api/page'

export const tagKinds = z.enum([
  'instance', 'volume', 'snapshot', 'network', 'subnet', 'security-group', 'security-group-rule',
  'elastic-ip', 'image', 'load-balancer', 'dns-zone', 'database-instance', 'cache-instance',
  'kubernetes-cluster', 'object-bucket', 'region', 'zone',
])

const keySchema = z.string().min(1).refine((key) =>
  key.trim() === key && new TextEncoder().encode(key).length <= 128 && !/\p{Cc}/u.test(key))

const ruleSchema = z.object({
  kind: tagKinds,
  required_keys: z.array(keySchema).min(1).max(24).refine((keys) => new Set(keys).size === keys.length),
})

export const tagPolicyInput = z.object({
  max_age_seconds: z.int().min(60).max(604800),
  rules: z.array(ruleSchema).max(24).refine((rules) => new Set(rules.map((rule) => rule.kind)).size === rules.length),
})

export const tagPolicySchema = tagPolicyInput.extend({
  revision: z.int().positive(), updated_at: z.iso.datetime({ offset: true }),
})

export const tagState = z.enum(['compliant', 'noncompliant', 'unknown'])
const tagEvidence = z.enum(['unobserved', 'missing_candidate', 'account_unavailable', 'credentials_changed', 'observation_expired'])
export const tagReportSchema = pageSchema(z.object({
  account: z.string(), provider: z.string(), region: z.string(), kind: tagKinds,
  id: z.string(), name: z.string(), state: tagState, reason: tagEvidence.optional(),
  tags_observed_at: z.iso.datetime({ offset: true }).optional(), missing_keys: z.array(keySchema).max(24).optional(),
})).extend({ policy_revision: z.int().positive() })

export type TagPolicy = z.infer<typeof tagPolicySchema>
export type TagPolicyInput = z.infer<typeof tagPolicyInput>
export type TagReport = z.infer<typeof tagReportSchema>
