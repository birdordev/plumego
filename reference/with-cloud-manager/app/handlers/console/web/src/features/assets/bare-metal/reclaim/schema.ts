import { z } from 'zod'
import { revision, time } from '../../../../shared/api/fields'

export const operationID = z.string().regex(/^[a-zA-Z0-9][a-zA-Z0-9_.:/-]{0,127}$/)
export const evidenceDigest = z.string().regex(/^[0-9a-f]{64}$/)
export const decisionSchema = z.enum(['approve', 'reject'])

const recoverySchema = z.object({
  id: operationID,
  isolation_sha256: evidenceDigest,
  evidence_sha256: evidenceDigest,
  state: z.enum(['requested', 'verified', 'rejected']),
  requester_id: operationID,
  reviewer_id: operationID.optional(),
  requested_at: time,
})

export const reclaimSchema = z.object({
  id: operationID,
  state: z.enum(['requested', 'approved', 'rejected', 'claimed', 'verified', 'consumed']),
  ownership_revision: revision,
  sanitizer_id: operationID,
  requester_id: operationID,
  reviewer_id: operationID.optional(),
  evidence_sha256: evidenceDigest.optional(),
  requested_at: time,
  claimed_at: time.optional(),
  verified_at: time.optional(),
  recovery: recoverySchema.optional(),
})

export const currentReclaimSchema = z.object({ reclaim: reclaimSchema.nullable() })
export const transferredOwnershipSchema = z.object({ project: z.string().min(1), revision })
export const evidenceSchema = z.object({ isolation_sha256: evidenceDigest, evidence_sha256: evidenceDigest })

export type Reclaim = z.infer<typeof reclaimSchema>
export type Decision = z.infer<typeof decisionSchema>
export type Evidence = z.infer<typeof evidenceSchema>
export type TransferredOwnership = z.infer<typeof transferredOwnershipSchema>
