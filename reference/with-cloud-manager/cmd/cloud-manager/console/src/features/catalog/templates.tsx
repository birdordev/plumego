import { DetailFields, type DetailField } from '../../shared/components/DetailFields'
import { openDetail } from '../../shared/detail/store'
import { useDetailAction } from '../../shared/detail/useDetailAction'
import { catalogFields, timestamp } from '../../shared/display/labels'
import { request } from '../session/client'
import { canExecute, projectURL } from '../session/project'
import { sessionSnapshot } from '../session/store'
import { catalogCollection } from './collection'
import { showCatalogEditor } from './editor'
import { showCatalogHistory } from './history'
import { LaunchForm } from './launch'
import { catalogTemplateSchema, type CatalogTemplate } from './schema'

export function showCatalogTemplate(item: CatalogTemplate) {
  const base = projectURL() + '/service-catalog/' + encodeURIComponent(item.id)
  const admin = sessionSnapshot()?.project.role === 'admin'
  openDetail(item.title, (stamp) => <TemplatePanel base={base} stamp={stamp} item={item} admin={admin} launchable={canExecute()} />)
}

function TemplatePanel({ base, stamp, item, admin, launchable }:
  { base: string; stamp: number; item: CatalogTemplate; admin: boolean; launchable: boolean }) {
  const { busy, run } = useDetailAction(stamp)
  const fields: DetailField[] = [['模板 ID', item.id], ['发布版本', item.version], ['状态', item.enabled ? '启用' : '已停用'],
    ...Object.entries(catalogFields).slice(1).map(([field, label]): DetailField => [label, item[field as keyof typeof catalogFields]]),
    ['发布时间', timestamp(item.published_at)]]

  function retire() {
    if (!window.confirm('停用后将停止受理使用此模板的新申请。确定停用？')) return
    void run(() => request(base, { method: 'DELETE', match: item.revision }, catalogTemplateSchema),
      (saved) => { void catalogCollection.refresh(); showCatalogTemplate(saved) })
  }

  return <>
    <DetailFields fields={fields} />
    {launchable && item.enabled ? <LaunchForm base={base} item={item} stamp={stamp} /> : null}
    {admin ? <div className="row-actions">
      <button type="button" onClick={() => showCatalogEditor(item, showCatalogTemplate)}>发布新版本</button>
      <button type="button" onClick={() => showCatalogHistory(item, base)}>查看历史版本</button>
      {item.enabled ? <button type="button" disabled={busy} onClick={retire}>停用模板</button> : null}
    </div> : null}
  </>
}
