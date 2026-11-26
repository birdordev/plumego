import { useSyncExternalStore } from 'react'
import { catalogCollection } from './collection'
import { subscribeCatalogRetry } from './attempts'
import { canRetryCatalogLaunch, retryPendingCatalogLaunch } from './launch'
import { showCatalogEditor } from './editor'
import { showCatalogTemplate } from './templates'

export function CatalogTools({ admin }: { admin: boolean }) {
  const canRetry = useSyncExternalStore(subscribeCatalogRetry, canRetryCatalogLaunch)
  return (
    <div id="catalog-tools" className="filters">
      {admin ? <button id="catalog-create" onClick={() => showCatalogEditor(undefined, showCatalogTemplate)} className="primary" type="button">发布模板</button> : null}
      <button id="catalog-refresh" onClick={() => void catalogCollection.refresh()} type="button">刷新目录</button>
      {canRetry ? <button id="catalog-retry" onClick={retryPendingCatalogLaunch} type="button">重试待确认的申请</button> : null}
    </div>
  )
}
