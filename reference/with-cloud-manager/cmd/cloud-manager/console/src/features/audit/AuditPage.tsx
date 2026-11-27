import { auditCollection } from './collection'
import { CollectionPage } from '../../shared/collection/CollectionPage'
import { useCollection } from '../../shared/collection/useCollection'
import { AuditTable } from './AuditTable'
import { AuditExport } from './AuditExport'
import { CollectionFilters } from '../../shared/collection/CollectionFilters'
import { AuditFilters } from './filters'

export default function AuditPage({ nativeConsole, nativeSerial }: { nativeConsole: boolean; nativeSerial: boolean }) {
  const page = useCollection(auditCollection)
  if (!page) return null
  const filters = <CollectionFilters values={page.filters} onApply={auditCollection.applyFilters} dateFields={['from', 'until']}>
    {(values) => <AuditFilters values={values} />}
  </CollectionFilters>
  return <CollectionPage {...page} onLoadNext={auditCollection.loadNext} filters={filters} footer={<AuditExport />}>
    <AuditTable items={page.items} nativeEnabled={nativeConsole || nativeSerial} />
  </CollectionPage>
}
