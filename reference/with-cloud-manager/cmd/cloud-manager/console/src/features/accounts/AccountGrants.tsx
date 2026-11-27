import { PagedResults } from '../../shared/collection/PagedResults'
import { ConsoleTable } from '../../shared/components/ConsoleTable'
import { listGrants } from './api'

export function AccountGrants({ target, signal, revoke }: {
  target: string; signal: AbortSignal; revoke?: (project: string) => void
}) {
  const columns = ['共享项目', '权限']
  if (revoke) columns.push('操作')
  return <section aria-label="账号共享授权">
    <h3>共享授权</h3>
    <PagedResults target={target} signal={signal} read={listGrants}>{(items) => items.length
      ? <ConsoleTable columns={columns} rows={items.map((item) => ({
        key: item.project,
        cells: [item.project, item.allow_write ? '读写' : '只读', ...(revoke ? [
          <button type="button" onClick={() => revoke(item.project)} aria-label={'撤销共享 ' + item.project}>撤销共享</button>,
        ] : [])],
      }))} />
      : <p>没有共享授权。</p>}</PagedResults>
  </section>
}
