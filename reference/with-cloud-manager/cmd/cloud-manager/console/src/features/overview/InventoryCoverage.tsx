import { requestErrorMessage } from '../../shared/api/requests'
import { ConsoleIcon } from '../../shared/components/ConsoleIcon'
import { timestamp } from '../../shared/display/time'
import { freshnessStates } from '../governance/freshness/labels'
import type { WorkspaceLink } from './navigation'
import type { OverviewFreshness } from './useOverview'

const formatCount = new Intl.NumberFormat('zh-CN')
const formatPercent = new Intl.NumberFormat('zh-CN', { style: 'percent', maximumFractionDigits: 1 })

export function InventoryCoverage({ result, link }: { result: OverviewFreshness; link: WorkspaceLink }) {
  const report = result.state === 'ready' ? result.data : undefined
  const summary = report?.summary
  const coverage = summary?.total ? summary.fresh / summary.total : undefined

  return <section className="overview-coverage" aria-labelledby="coverage-title">
    <div className="overview-heading">
      <h2 id="coverage-title"><ConsoleIcon name="time" />资产盘点</h2>
      {report && <p className="hint">统计于 <time dateTime={report.as_of}>{timestamp(report.as_of)}</time> · 窗口 {formatCount.format(report.window_seconds)} 秒</p>}
    </div>
    {result.state === 'loading' && <p className="hint" role="status">正在读取盘点覆盖…</p>}
    {result.state === 'error' && <p className="overview-error" role="alert">{requestErrorMessage(result.error)}</p>}
    {summary && <>
      <div className="overview-metrics">
        <a {...link('inventory-freshness')}>
          <span>全部盘点范围</span><strong>{formatCount.format(summary.total)}</strong>
        </a>
        {freshnessStates.map(([state, label]) => (
          <a key={state} data-tone={state} {...link('inventory-freshness', 'state=' + state)}>
            <span>{label}</span><strong>{formatCount.format(summary[state])}</strong>
          </a>
        ))}
      </div>
      {coverage !== undefined ? <label className="overview-coverage-track">
        <span className="hint">近期完成比例 <strong>{formatPercent.format(coverage)}</strong></span>
        <meter min={0} max={1} value={coverage} />
      </label> : <p className="hint">当前项目暂无启用的全量盘点计划。</p>}
    </>}
  </section>
}
