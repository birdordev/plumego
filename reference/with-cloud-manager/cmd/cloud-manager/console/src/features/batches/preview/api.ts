import { request } from '../../session/client'
import { batchPreviewSchema } from './schema'

export function previewBatch(base: string, body: string, signal: AbortSignal) {
  return request(base + '/operation-batches/preview', { method: 'POST', body, signal }, batchPreviewSchema)
}
