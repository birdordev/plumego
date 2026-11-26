import { ConsoleRequestError } from '../../shared/api/requests'
import { DetailForm } from '../../shared/detail/DetailForm'
import { formText } from '../../shared/detail/form-values'
import { openDetail } from '../../shared/detail/store'
import { showOperation } from '../operations/detail/open'
import { operationReceiptSchema } from '../operations/schema'
import { request } from '../session/client'
import { projectURL } from '../session/project'
import { sessionSnapshot } from '../session/store'
import { catalogAttempt, type LaunchAttempt, notifyCatalogRetry, pendingCatalogLaunch, removeCatalogAttempt } from './attempts'
import type { CatalogTemplate } from './schema'

const namePattern = '[a-zA-Z0-9][a-zA-Z0-9 _.:\\-]{0,79}'

function currentPendingLaunch() {
  return sessionSnapshot() ? pendingCatalogLaunch(projectURL()) : undefined
}

export function canRetryCatalogLaunch() {
  return Boolean(currentPendingLaunch())
}

export function retryPendingCatalogLaunch() {
  const attempt = currentPendingLaunch()
  if (!attempt) return
  openDetail('重试实例申请', (stamp) => <>
    <p>{attempt.name}</p>
    <DetailForm stamp={stamp} submitLabel="使用原幂等键重试" onSubmit={() => sendCatalogLaunch(attempt)}
      onSaved={(receipt) => showOperation(receipt.id)} />
  </>)
}

export function LaunchForm({ base, item, stamp }: { base: string; item: CatalogTemplate; stamp: number }) {
  return <DetailForm stamp={stamp} submitLabel="提交实例申请" onSubmit={(data) => {
    const name = formText(data, 'name').trim()
    const body = JSON.stringify({ version: item.version, name })
    return sendCatalogLaunch(catalogAttempt(projectURL(), base + '/launch', body, name))
  }} onSaved={(receipt) => showOperation(receipt.id)}>
    <label>实例名称<input name="name" required maxLength={80} pattern={namePattern} /></label>
  </DetailForm>
}

async function sendCatalogLaunch(attempt: LaunchAttempt) {
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
