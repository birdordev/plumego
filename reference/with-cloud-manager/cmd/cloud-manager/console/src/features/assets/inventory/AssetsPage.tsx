import { assetsCollection } from './collection'
import { CollectionFilters } from '../../../shared/collection/CollectionFilters'
import { CollectionPage } from '../../../shared/collection/CollectionPage'
import { useCollection } from '../../../shared/collection/useCollection'
import { AssetsTable } from './AssetsTable'
import { showElasticIPAllocation } from '../elastic-ips/allocation'
import { AssetFilters } from './filters'
import { SelectedInstances } from '../instances/SelectedInstances'

type Props = { canExecute: boolean; nativeConsole: boolean; nativeSerial: boolean; onBatch: () => void }

export default function AssetsPage({ canExecute, nativeConsole, nativeSerial, onBatch }: Props) {
  const page = useCollection(assetsCollection)
  if (!page) return null
  const tools = canExecute ? <div id="asset-tools" className="filters">
    <button id="allocate-elastic-ip" onClick={showElasticIPAllocation} type="button" className="primary">申请弹性公网 IP</button>
  </div> : null
  return <CollectionPage {...page} onLoadNext={assetsCollection.loadNext} tools={tools}
    filters={<CollectionFilters onApply={assetsCollection.applyFilters}><AssetFilters /></CollectionFilters>}
    footer={canExecute ? <SelectedInstances onBatch={onBatch} /> : null}>
    <AssetsTable items={page.items} editable={canExecute} nativeConsole={nativeConsole} nativeSerial={nativeSerial} />
  </CollectionPage>
}
