import { grantsCollection } from './collection'
import { CollectionPage } from '../../shared/collection/CollectionPage'
import { useCollection } from '../../shared/collection/useCollection'
import { GrantsTable } from './GrantsTable'

function RefreshGrants() {
  return <div id="native-tools" className="filters">
    <button id="native-refresh" onClick={() => void grantsCollection.refresh()} type="button">刷新访问申请</button>
  </div>
}

export default function GrantsPage() {
  const page = useCollection(grantsCollection)
  if (!page) return null
  return <CollectionPage {...page} onLoadNext={grantsCollection.loadNext} tools={<RefreshGrants />}>
    <GrantsTable items={page.items} />
  </CollectionPage>
}
