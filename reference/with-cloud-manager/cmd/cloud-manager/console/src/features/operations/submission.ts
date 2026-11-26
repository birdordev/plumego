import { request } from '../session/client'
import { operationAttempt, removeOperationAttempt } from './attempts'
import { operationReceiptSchema } from './schema'

export async function requestVersionedOperation(target: string, body: string, match: number | undefined) {
  const identity = JSON.stringify([target, body, match])
  const key = operationAttempt(identity)
  const receipt = await request(target, { method: 'POST', body, match, key }, operationReceiptSchema)
  removeOperationAttempt(identity, key)
  return receipt
}
