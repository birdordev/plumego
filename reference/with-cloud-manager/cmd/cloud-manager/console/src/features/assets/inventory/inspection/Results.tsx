import { timestamp } from '../../../../shared/display/time'
import { statuses } from '../../../../shared/display/status-labels'
import { kinds } from '../../labels'
import { volumeSummaryText } from '../../volumes/VolumeSummary'
import type { AssetHistoryPage, AssetTopologyPage } from './schema'

const changes: Readonly<Record<string, string>> = {
  discovered: '新发现', changed: '更新', missing_candidate: '待确认缺失', rediscovered: '重新发现', removed: '确认移除',
}

export function AssetHistory({ page }: { page: AssetHistoryPage }) {
  if (!page.items.length) return <p>没有匹配的资产变化。</p>
  return <div className="table-scroll"><table>
    <thead><tr><th scope="col">时间</th><th scope="col">事件</th><th scope="col">资产</th><th scope="col">状态</th></tr></thead>
    <tbody>{page.items.map((row, index) => {
      const before = volumeSummaryText(row.previous_volume)
      const after = volumeSummaryText(row.volume)
      const volume = before && before !== after ? `${before} → ${after || '暂无属性'}` : after
      return <tr key={JSON.stringify([row.account, row.region, row.kind, row.asset_id, row.occurred_at, index])}>
        <td>{timestamp(row.occurred_at)}</td>
        <td>{changes[row.action] ?? row.action}</td>
        <td>{row.previous_name && row.previous_name !== row.name ? `${row.previous_name} → ` : ''}{row.name || row.asset_id}
          <div className="minor">{row.account} / {row.region || '全局'} · {row.asset_id}</div>
          {volume ? <div className="minor">{volume}</div> : null}</td>
        <td>{row.previous_status && row.previous_status !== row.status ? `${statuses[row.previous_status] ?? row.previous_status} → ` : ''}{(statuses[row.status] ?? row.status) || '—'}</td>
      </tr>
    })}</tbody>
  </table></div>
}

const relations: Readonly<Record<string, string>> = {
  uses_volume: '使用磁盘', attached_to: '挂载到', uses_image: '使用镜像', in_network: '所属网络',
  in_subnet: '所属子网', uses_security_group: '使用安全组',
}

export function AssetTopology({ page }: { page: AssetTopologyPage }) {
  if (!page.nodes.length) return <p>没有匹配的资产。</p>
  return <ul>{page.nodes.map(({ asset, links }) => <li key={JSON.stringify([asset.account, asset.region, asset.kind, asset.id])}>
    <strong>{asset.name || asset.id}</strong>
    <div className="minor">{asset.account} / {asset.region || '全局'} · {asset.id}</div>
    {links.length ? <ul>{links.map((link) => <li key={JSON.stringify([link.kind, link.id, link.relation])}>
      {relations[link.relation] ?? link.relation} · {kinds[link.kind] ?? link.kind} · {link.name || link.id}
      {link.name && link.name !== link.id ? <span className="minor">{link.id}</span> : null}
    </li>)}</ul> : <p>未观测到可见的关联资源。</p>}
  </li>)}</ul>
}
