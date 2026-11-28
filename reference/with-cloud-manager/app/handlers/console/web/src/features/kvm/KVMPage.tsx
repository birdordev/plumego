import { CollectionFilters } from '../../shared/collection/CollectionFilters'
import { CollectionPage } from '../../shared/collection/CollectionPage'
import { useCollection } from '../../shared/collection/useCollection'
import type { Project } from '../session/store'
import { inventoryKind } from './api'
import { kvmCollection } from './collection'
import { InventoryTable } from './InventoryTable'
import { showVMByID } from './detail/open'
import { openProvisioning } from './provisioning/open'
import { siteIDPattern, type InventoryKind } from './schema'

const kinds = [['vms', '虚拟机'], ['hosts', '宿主机']] as const

export default function KVMPage({ role, principal }: { role: Project['role']; principal: string }) {
  const page = useCollection(kvmCollection)
  if (!page?.scope) return null
  const filters = new URLSearchParams(page.filters)
  const kind = inventoryKind(filters)
  const { base } = page.scope

  function selectKind(next: InventoryKind) {
    if (next === kind) return
    if (next === 'hosts') filters.set('kind', next)
    else filters.delete('kind')
    void kvmCollection.applyFilters(filters.toString())
  }

  return <CollectionPage {...page} onLoadNext={kvmCollection.loadNext}
    tools={<div className="collection-tools">
      <div className="view-switcher" role="group" aria-label="KVM 资源类型">
        {kinds.map(([name, title]) => <button key={name} type="button" aria-pressed={name === kind}
          onClick={() => selectKind(name)}>{title}</button>)}
      </div>
      {kind === 'vms' && (role === 'operator' || role === 'admin') && <button type="button" className="primary"
        onClick={() => openProvisioning({ base, principal, site: filters.get('site') ?? undefined, openVM: (site, id) => {
          void kvmCollection.refresh()
          showVMByID(base, site, id, role)
        } })}>创建 KVM 虚拟机</button>}
    </div>}
    filters={<CollectionFilters values={page.filters} clearTo={kind === 'hosts' ? 'kind=hosts' : ''}
      onApply={kvmCollection.applyFilters}>{(values) => <>
      <input type="hidden" name="kind" value={kind === 'hosts' ? kind : ''} />
      <label>站点 ID<input name="site" defaultValue={values.get('site') ?? ''} maxLength={64} pattern={siteIDPattern} /></label>
    </>}</CollectionFilters>}>
    <InventoryTable base={page.scope.base} kind={kind} items={page.items} role={role} />
  </CollectionPage>
}
