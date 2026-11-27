import { alertsCollection } from './collection'
import { CollectionPage } from '../../shared/collection/CollectionPage'
import { useCollection } from '../../shared/collection/useCollection'
import { AlertsTable } from './AlertsTable'
import { CollectionFilters } from '../../shared/collection/CollectionFilters'
import { AlertFilters } from './filters'

export default function AlertsPage({ admin }: { admin: boolean }) {
  const page = useCollection(alertsCollection)
  if (!page) return null
  const filters = <CollectionFilters values={page.filters} onApply={alertsCollection.applyFilters}>
    {(values) => <AlertFilters values={values} admin={admin} />}
  </CollectionFilters>
  return <CollectionPage {...page} onLoadNext={alertsCollection.loadNext} filters={filters}>
    <AlertsTable items={page.items} />
  </CollectionPage>
}
