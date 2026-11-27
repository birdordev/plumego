import { ConsoleTable } from '../../shared/components/ConsoleTable'
import { timestamp } from '../../shared/display/time'
import type { Project } from '../session/store'
import { showInventoryDetail } from './detail/open'
import { hostStatus, vmStatus } from './labels'
import type { InventoryItem, InventoryKind } from './schema'

const columns = {
  hosts: ['宿主机', '站点', '故障域', '状态', '最近上报'],
  vms: ['虚拟机', '站点', '宿主机', '状态', 'IPv4'],
}

export function InventoryTable({ base, kind, items, role }: {
  base: string; kind: InventoryKind; items: readonly InventoryItem[]; role: Project['role']
}) {
  return <ConsoleTable columns={columns[kind]} rows={items.map((item) => ({
    key: item.site + '/' + item.id,
    cells: [
      <button type="button" onClick={() => showInventoryDetail(base, item, role)}>{item.id}</button>,
      item.site,
      ...(item.kind === 'hosts'
        ? [item.failure_domain, hostStatus(item), timestamp(item.reported_at)]
        : [item.host, vmStatus(item.state), item.ipv4]),
    ],
  }))} />
}
