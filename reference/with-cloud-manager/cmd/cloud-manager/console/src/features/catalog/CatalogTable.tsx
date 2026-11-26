import type { z } from 'zod'
import type { catalogTemplateSchema } from './schema'
import { RowAction } from '../../shared/components/RowAction'
import { ConsoleTable } from '../../shared/components/ConsoleTable'
import { showCatalogTemplate } from './templates'

type Template = z.infer<typeof catalogTemplateSchema>

export function CatalogTable({ items }: { items: readonly Template[] }) {
  const columns = ['模板', '版本', '账号 / 地域', '规格', '状态', '操作']
  const rows = items.map((item) => ({
    key: item.id,
    cells: [
      <div>{item.title}<span className="minor">{item.id}</span></div>,
      item.version, item.account + ' / ' + item.region, item.size, item.enabled ? '启用' : '已停用',
      <RowAction onClick={() => showCatalogTemplate(item)}>查看</RowAction>,
    ],
  }))
  return <ConsoleTable columns={columns} rows={rows} />
}
