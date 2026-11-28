import { operationsCollection } from './collection'
import { CollectionPage } from '../../../shared/collection/CollectionPage'
import { useCollection } from '../../../shared/collection/useCollection'
import { OperationsTable } from './OperationsTable'
import { CollectionFilters } from '../../../shared/collection/CollectionFilters'
import { OperationFilters } from './filters'
import { OperationBacklogButton } from '../backlog/OperationBacklogButton'
import { OperationBudgetButton } from '../budget/OperationBudgetButton'

export default function OperationsPage() {
  const page = useCollection(operationsCollection)
  if (!page) return null
  const filters = <CollectionFilters values={page.filters} onApply={operationsCollection.applyFilters}>
    {(values) => <OperationFilters values={values} />}
  </CollectionFilters>
  return <CollectionPage {...page} onLoadNext={operationsCollection.loadNext} filters={filters}
    tools={<><OperationBacklogButton /><OperationBudgetButton /></>}>
    <OperationsTable items={page.items} showQueueIssue={new URLSearchParams(page.filters).has('queue_issue')} />
  </CollectionPage>
}
