import { DetailForm } from '../../shared/detail/DetailForm'
import { openDetail } from '../../shared/detail/store'
import { catalogFields } from '../../shared/display/labels'
import { request } from '../session/client'
import { projectURL } from '../session/project'
import { catalogCollection } from './collection'
import { catalogTemplateSchema, type CatalogTemplate } from './schema'

const idPattern = '[a-z0-9][a-z0-9\\-]{0,63}'

export function showCatalogEditor(item: CatalogTemplate | undefined, onSaved: (item: CatalogTemplate) => void) {
  const target = projectURL() + '/service-catalog' + (item ? '/' + encodeURIComponent(item.id) : '')
  openDetail(item ? '发布新版本' : '发布模板', (stamp) => (
    <DetailForm stamp={stamp} submitLabel="确认发布" onSubmit={(data) => request(target, {
      body: JSON.stringify(Object.fromEntries(data)), method: item ? 'PUT' : 'POST', match: item?.revision,
    }, catalogTemplateSchema)} onSaved={(saved) => { void catalogCollection.refresh(); onSaved(saved) }}>
      {!item ? <label>模板 ID<input name="id" required maxLength={64} pattern={idPattern} /></label> : null}
      {Object.entries(catalogFields).map(([field, label]) => <label key={field}>{label}
        <input name={field} required defaultValue={item?.[field as keyof typeof catalogFields]} maxLength={field === 'title' ? 120 : 128} />
      </label>)}
    </DetailForm>
  ))
}
