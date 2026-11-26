import { catalogCollection } from './collection'
import { CollectionPage } from '../../shared/collection/CollectionPage'
import { useCollection } from '../../shared/collection/useCollection'
import { CatalogTable } from './CatalogTable'
import { CatalogTools } from './CatalogTools'

export default function CatalogPage({ admin }: { admin: boolean }) {
  const page = useCollection(catalogCollection)
  if (!page) return null
  return <CollectionPage {...page} onLoadNext={catalogCollection.loadNext} tools={<CatalogTools admin={admin} />}>
    <CatalogTable items={page.items} />
  </CollectionPage>
}
