import { ConsoleTable } from '../../../shared/components/ConsoleTable'
import { timestamp } from '../../../shared/display/labels'
import type { TagReport } from './schema'

const states = { compliant: '合规', noncompliant: '缺少必填标签', unknown: '证据不足' }
const reasons = { unobserved: '未返回标签数据', missing_candidate: '盘点待确认缺失', account_unavailable: '账号不可用',
  credentials_changed: '凭据已轮换', observation_expired: '证据已过期' }
const columns = ['资源', '账号 / 地域', '结果', '证据时间', '缺少的标签键']

export function TagComplianceTable({ items }: { items: readonly TagReport['items'][number][] }) {
  const rows = items.map((item) => ({
    key: JSON.stringify([item.account, item.region, item.kind, item.id]),
    cells: [
      <div>{item.name || item.id}{item.name && item.name !== item.id ? <span className="minor">{item.id}</span> : null}</div>,
      item.region ? item.account + ' / ' + item.region : item.account,
      item.reason ? states[item.state] + ' · ' + reasons[item.reason] : states[item.state],
      item.tags_observed_at ? timestamp(item.tags_observed_at) : '—',
      item.missing_keys?.join('、') ?? '—',
    ],
  }))
  return <ConsoleTable columns={columns} rows={rows} />
}
