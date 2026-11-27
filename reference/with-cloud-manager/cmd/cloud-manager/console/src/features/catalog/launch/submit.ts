import { ConsoleRequestError } from '../../../shared/api/requests'
import { operationReceiptSchema } from '../../operations/forms/schema'
import { request } from '../../session/client'
import { notifyCatalogRetry, removeCatalogAttempt, type LaunchAttempt } from './attempts'

export async function sendCatalogLaunch(attempt: LaunchAttempt) {
  if (attempt.running) throw new Error('此申请还在提交，请稍后重试。')
  attempt.running = true
  try {
    const receipt = await request(attempt.target, { method: 'POST', body: attempt.body, key: attempt.key }, operationReceiptSchema)
    removeCatalogAttempt(attempt)
    return receipt
  } catch (error) {
    attempt.uncertain = !(error instanceof ConsoleRequestError) || error.status >= 500
    throw error
  } finally {
    attempt.running = false
    notifyCatalogRetry()
  }
}
