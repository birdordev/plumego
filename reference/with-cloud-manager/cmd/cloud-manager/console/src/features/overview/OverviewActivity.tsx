import { showAlert } from '../alerts/detail'
import { alertRules } from '../alerts/labels'
import { showOperation } from '../operations/detail/open'
import { actions } from '../operations/labels'
import { ActivityPanel } from './ActivityPanel'
import type { WorkspaceLink } from './navigation'
import type { OverviewAlerts, OverviewOperations } from './useOverview'

export function OverviewActivity({ operations, alerts, link, onReload }: {
  operations: OverviewOperations; alerts: OverviewAlerts; link: WorkspaceLink; onReload: () => void
}) {
  const recentOperations = operations.state === 'ready' ? operations.data.items.map((item) => ({
    id: item.id, title: actions[item.action as keyof typeof actions] || item.action,
    subject: item.asset_id || '待分配资源', at: item.created_at, status: item.status,
  })) : []
  const activeAlerts = alerts.state === 'ready' ? alerts.data.items.map((item) => ({
    id: item.id, title: alertRules[item.rule as keyof typeof alertRules] || item.rule,
    subject: [item.account, item.region, item.subject_id].filter(Boolean).join(' / '),
    at: item.opened_at, status: item.status,
  })) : []
  return <div className="overview-panels">
    <ActivityPanel id="recent-operations" title="近期操作" icon="activity" empty="当前项目暂无操作记录。"
      target={link('operations')} items={recentOperations} pending={operations.state === 'loading'}
      error={operations.state === 'error' ? operations.error : undefined} showRecord={(id) => showOperation(id, onReload)} />
    <ActivityPanel id="active-alerts" title="未恢复告警" icon="bell" empty="当前项目没有未恢复告警。"
      target={link('alerts', 'status=active')} items={activeAlerts} pending={alerts.state === 'loading'}
      error={alerts.state === 'error' ? alerts.error : undefined} showRecord={(id) => showAlert(id, onReload)} />
  </div>
}
