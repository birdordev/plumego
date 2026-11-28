import { operationsCollection } from '../history/collection'
import { showOperationBacklog } from './open'

export function OperationBacklogButton() {
  function openBacklog() {
    const { scope, filters } = operationsCollection.snapshot()
    if (scope) showOperationBacklog(scope.base, filters)
  }

  return <button type="button" onClick={openBacklog}>查看操作积压</button>
}
