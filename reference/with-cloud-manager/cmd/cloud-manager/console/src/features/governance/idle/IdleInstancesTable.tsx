import { ConsoleTable } from '../../../shared/components/ConsoleTable'
import { RowAction } from '../../../shared/components/RowAction'
import { formatCPU, formatNetwork, showIdleEvidence } from './evidence'
import type { IdleInstance } from './schema'

const states = { candidate: '低活动候选', active: '观察到活动', insufficient: '证据不足', not_running: '未运行' }
const reasons = { inventory_stale: '盘点证据过期', instance_not_running: '实例未运行',
  metrics_missing: '尚未采集指标', binding_changed: '账号或归属已变更', metrics_stale: '监控窗口已过期',
  incomplete_metrics: '采样有缺口' }
const columns = ['实例', '账号 / 地域', '分析结果', 'CPU 峰值', '网络峰值', '证据']

export function IdleInstancesTable({ items }: { items: readonly IdleInstance[] }) {
  const rows = items.map((row) => ({
    key: JSON.stringify([row.account, row.region, row.id]),
    cells: [
      <div>{row.name || row.id}{row.name && row.name !== row.id ? <span className="minor">{row.id}</span> : null}</div>,
      row.account + ' / ' + row.region,
      row.reason && row.reason !== 'instance_not_running' ? states[row.state] + ' · ' + reasons[row.reason] : states[row.state],
      formatCPU(row.evidence?.cpu_peak_percent),
      formatNetwork(row.evidence?.network_peak_bytes_per_second),
      row.evidence ? <RowAction onClick={() => showIdleEvidence(row)}>查看证据</RowAction> : '—',
    ],
  }))
  return <ConsoleTable columns={columns} rows={rows} />
}
