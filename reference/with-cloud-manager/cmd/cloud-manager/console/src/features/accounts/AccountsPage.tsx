import { useState } from 'react'
import { PagedResults } from '../../shared/collection/PagedResults'
import { ConsoleTable } from '../../shared/components/ConsoleTable'
import { StateBadge } from '../../shared/components/RowAction'
import { providerName } from '../../shared/display/providers'
import { listAccounts } from './api'
import { modes } from './labels'
import { openAccount, openNewAccount } from './open'

type Props = { base: string; project: string; projects: string[] }

export default function AccountsPage(props: Props) {
  return <ProjectAccounts key={props.base} {...props} />
}

function ProjectAccounts({ base, project, projects }: Props) {
  const [refresh, setRefresh] = useState(0)
  const target = base + '/cloud-accounts'
  const context = { target, project, projects, onChanged: () => setRefresh((value) => value + 1) }
  return <>
    <div className="row-actions">
      <button type="button" className="primary" onClick={() => openNewAccount(context)}>登记云账号</button>
      <button type="button" onClick={context.onChanged}>刷新账号列表</button>
    </div>
    <PagedResults key={refresh} target={target} read={listAccounts}>{(items) => items.length
      ? <ConsoleTable columns={['账号', '厂商', '状态', '版本']} rows={items.map((item) => ({
        key: item.id,
        cells: [<button type="button" onClick={() => openAccount(context, item.id)}>{item.id}</button>,
          providerName(item.provider), <StateBadge status={item.mode} label={modes[item.mode]} />, item.revision],
      }))} />
      : <p>当前项目没有自有云账号。</p>}</PagedResults>
  </>
}
