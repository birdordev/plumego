import type { ComponentProps } from 'react'
import { ConsoleTable } from '../../../shared/components/ConsoleTable'
import { MetricStrip } from '../../../shared/components/MetricStrip'
import { providerName } from '../../../shared/display/providers'
import { timestamp } from '../../../shared/display/time'
import type { DatabaseEvidence, DatabaseProtectionReport } from './api'

const statusLabels = {
  ready: '证据有效', outdated: '证据过旧', missing: '无可用证据',
  stale: '观测过期', unobserved: '暂无观测', unsupported: '厂商产品不支持',
}
const outcomes = { success: '成功', failure: '失败', throttled: '限流' }

export type DatabaseAssetLink = (item: { id: string; account: string; region: string }) => Pick<ComponentProps<'a'>, 'href' | 'onClick'>

export function DatabaseProtectionResults({ report, assetLink }: { report: DatabaseProtectionReport; assetLink: DatabaseAssetLink }) {
  const { summary, items } = report
  const statuses = Object.keys(statusLabels) as (keyof typeof statusLabels)[]
  return <>
    <section aria-label="数据库恢复保障汇总">
      <MetricStrip values={[
        ['可见数据库', summary.total], ['资产盘点待确认', summary.inventory_unavailable], ['启用计划刷新异常', summary.refresh_failed],
      ]} />
      <p className="minor">统计时间 {timestamp(report.as_of)}</p>
      <ConsoleTable columns={['观测项', ...statuses.map((status) => statusLabels[status])]} rows={[
        { key: 'backups', cells: ['备份', ...statuses.map((status) => summary.backups[status])] },
        { key: 'restore-windows', cells: ['恢复窗口', ...statuses.map((status) => summary.restore_windows[status])] },
      ]} />
    </section>
    <section aria-label="数据库恢复保障明细">
      <ConsoleTable columns={['实例', '厂商', '账号及地域', '资产盘点', '备份', '恢复窗口']} rows={items.map((item) => ({
        key: [item.account, item.region, item.id].join('/'),
        cells: [<><a {...assetLink(item)}>{item.name || item.id}</a><span className="minor">{item.engine}</span></>,
          providerName(item.provider), <>{item.account}<span className="minor">{item.region}</span></>,
          item.inventory_available ? '可用' : '待确认',
          <Evidence value={item.backups} label="最近可用备份起始" />,
          <Evidence value={item.restore_windows} label="最近可恢复时间" />],
      }))} />
    </section>
  </>
}

function Evidence({ value, label }: { value: DatabaseEvidence; label: string }) {
  return <>
    {statusLabels[value.status]}
    {value.scope === 'source_instance' && <span className="minor">回档范围为源实例</span>}
    {value.latest_evidence_at && <span className="minor">{label} {timestamp(value.latest_evidence_at)}</span>}
    {value.observed_at && <span className="minor">观测 {timestamp(value.observed_at)}</span>}
    {value.schedule_enabled === false && <span className="minor">计划已停用</span>}
    {value.last_refresh_outcome && <span className="minor">最近刷新{outcomes[value.last_refresh_outcome]}
      {value.last_refresh_at && ' · ' + timestamp(value.last_refresh_at)}</span>}
  </>
}
