import { operationsCollection } from './collection'
import { CollectionPage } from '../../shared/collection/CollectionPage'
import { useCollection } from '../../shared/collection/useCollection'
import { OperationsTable } from './OperationsTable'
import { CollectionFilters } from '../../shared/collection/CollectionFilters'
import { OperationFilters } from './filters'

export default function OperationsPage() {
  const page = useCollection(operationsCollection)
  if (!page) return null
  return <CollectionPage {...page} onLoadNext={operationsCollection.loadNext} filters={<CollectionFilters onApply={operationsCollection.applyFilters}><OperationFilters /></CollectionFilters>}>
    <OperationsTable items={page.items} />
  </CollectionPage>
}
