import { request } from '../../session/client'
import { batchReceiptSchema } from './schema'

export function submitBatch(base: string, body: string, key: string) {
  return request(base + '/operation-batches', { method: 'POST', body, key }, batchReceiptSchema)
}

export function lookupBatch(base: string, id: string, signal: AbortSignal) {
  return request(base + '/operation-batches/' + encodeURIComponent(id), { signal }, batchReceiptSchema)
}
