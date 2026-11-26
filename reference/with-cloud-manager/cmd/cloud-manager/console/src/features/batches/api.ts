import { request } from '../session/client'
import { batchPreviewSchema, batchReceiptSchema } from './schema'

export function previewBatch(base: string, body: string, signal: AbortSignal) {
  return request(base + '/operation-batches/preview', { method: 'POST', body, signal }, batchPreviewSchema)
}

export function submitBatch(base: string, body: string, key: string) {
  return request(base + '/operation-batches', { method: 'POST', body, key }, batchReceiptSchema)
}

export function lookupBatch(base: string, id: string, signal: AbortSignal) {
  return request(base + '/operation-batches/' + encodeURIComponent(id), { signal }, batchReceiptSchema)
}
