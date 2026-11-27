import { assetsCollection } from './collection'
import { CollectionFilters } from '../../../shared/collection/CollectionFilters'
import { CollectionPage } from '../../../shared/collection/CollectionPage'
import { useCollection } from '../../../shared/collection/useCollection'
import { AssetsTable } from './AssetsTable'
import { showElasticIPAllocation } from '../elastic-ips/allocation'
import { AssetFilters } from './filters'
import { SelectedInstances } from '../instances/SelectedInstances'
import { AssetInspectionActions } from './AssetInspectionActions'

type Props = { canExecute: boolean; nativeConsole: boolean; nativeSerial: boolean; onBatch: () => void }

export default function AssetsPage({ canExecute, nativeConsole, nativeSerial, onBatch }: Props) {
  const page = useCollection(assetsCollection)
  if (!page) return null
  const filters = new URLSearchParams(page.filters)
  const viewingVolumes = filters.get('kind') === 'volume'

  function selectVolumeZone() {
    filters.set('kind', 'zone')
    filters.delete('q')
    void assetsCollection.applyFilters(filters.toString())
  }

  const tools = <div id="asset-tools" className="row-actions">
    {canExecute ? <button id="allocate-elastic-ip" onClick={showElasticIPAllocation} type="button" className="primary">申请弹性公网 IP</button> : null}
    {canExecute && viewingVolumes ? <button type="button" onClick={selectVolumeZone}>选择可用区创建云盘</button> : null}
    <AssetInspectionActions key={JSON.stringify([page.generation, page.filters])} />
  </div>
  return <CollectionPage {...page} onLoadNext={assetsCollection.loadNext} tools={tools}
    filters={<CollectionFilters values={page.filters} onApply={assetsCollection.applyFilters}>{(values) => <AssetFilters values={values} />}</CollectionFilters>}
    footer={canExecute ? <SelectedInstances onBatch={onBatch} /> : null}>
    <AssetsTable items={page.items} editable={canExecute} nativeConsole={nativeConsole} nativeSerial={nativeSerial} />
  </CollectionPage>
}
