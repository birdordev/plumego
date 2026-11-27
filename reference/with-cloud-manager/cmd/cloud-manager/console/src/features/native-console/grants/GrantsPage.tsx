import { grantsCollection } from './collection'
import { CollectionPage } from '../../../shared/collection/CollectionPage'
import { useCollection } from '../../../shared/collection/useCollection'
import { GrantsTable } from './GrantsTable'

export default function GrantsPage() {
  const page = useCollection(grantsCollection)
  if (!page) return null
  return <CollectionPage {...page} onLoadNext={grantsCollection.loadNext}>
    <GrantsTable items={page.items} />
  </CollectionPage>
}
