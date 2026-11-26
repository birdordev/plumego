import { alertsCollection } from './collection'
import { CollectionPage } from '../../shared/collection/CollectionPage'
import { useCollection } from '../../shared/collection/useCollection'
import { AlertsTable } from './AlertsTable'
import { CollectionFilters } from '../../shared/collection/CollectionFilters'
import { AlertFilters } from './filters'

export default function AlertsPage({ admin }: { admin: boolean }) {
  const page = useCollection(alertsCollection)
  if (!page) return null
  return <CollectionPage {...page} onLoadNext={alertsCollection.loadNext} filters={<CollectionFilters onApply={alertsCollection.applyFilters}><AlertFilters admin={admin} /></CollectionFilters>}>
    <AlertsTable items={page.items} />
  </CollectionPage>
}
