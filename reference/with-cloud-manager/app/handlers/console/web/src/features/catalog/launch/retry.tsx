import { DetailForm } from '../../../shared/detail/DetailForm'
import { openDetail } from '../../../shared/detail/store'
import { showOperation } from '../../operations/detail/open'
import { projectURL } from '../../session/project'
import { sessionSnapshot } from '../../session/store'
import { pendingCatalogLaunch } from './attempts'
import { sendCatalogLaunch } from './submit'

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
