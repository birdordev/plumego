import { z } from 'zod'
import { revision } from '../../../shared/api/fields'
import { request } from '../../session/client'

const previewSchema = z.object({
  system_disk_id: z.string().min(1),
  ownership_revision: revision,
})

export function readRetirementPreview(target: string, signal: AbortSignal) {
  return request(target, { signal }, previewSchema)
}
