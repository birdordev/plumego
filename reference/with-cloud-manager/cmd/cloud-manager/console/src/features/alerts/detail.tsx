import { requestErrorMessage } from '../../shared/api/requests'
import { DetailFields, type DetailField } from '../../shared/components/DetailFields'
import { StateBadge } from '../../shared/components/RowAction'
import { openDetail } from '../../shared/detail/store'
import { useDetailAction } from '../../shared/detail/useDetailAction'
import { useResourceRead } from '../../shared/api/useResourceRead'
import { alertRules } from './labels'
import { kinds } from '../assets/labels'
import { statuses } from '../../shared/display/status-labels'
import { timestamp } from '../../shared/display/time'
import { showOperation } from '../operations/detail/open'
import { request } from '../session/client'
import { projectURL } from '../session/project'
import { sessionSnapshot } from '../session/store'
import { alertsCollection } from './collection'
import { alertDetailSchema } from './schema'

function readAlert(target: string, signal: AbortSignal) {
  return request(target, { signal }, alertDetailSchema)
}

export function showAlert(id: string, onReload?: () => void) {
  const target = projectURL() + '/alerts/' + encodeURIComponent(id)
  const admin = sessionSnapshot()?.project.role === 'admin'
  openDetail('告警详情', (stamp, signal) => <AlertDetail target={target} admin={admin} stamp={stamp} signal={signal}
    onReload={onReload} reload={() => {
    onReload?.()
    showAlert(id, onReload)
  }} />)
}

function AlertDetail({ target, admin, stamp, signal, reload, onReload }:
  { target: string; admin: boolean; stamp: number; signal: AbortSignal; reload: () => void; onReload?: () => void }) {
  const result = useResourceRead(target, readAlert, signal)
  const { busy, run } = useDetailAction(stamp)
  if (result.state === 'loading') return <p role="status">正在加载…</p>
  if (result.state === 'error') return <p role="alert" className="notice" data-error="true">{requestErrorMessage(result.error)}</p>

  const alert = result.data
  const fields: DetailField[] = [['规则', alertRules[alert.rule] || alert.rule], ['状态', <StateBadge status={alert.status} />],
    ['触发时间', timestamp(alert.opened_at)], ['触发状态', statuses[alert.source_status] || alert.source_status],
    ['云账号', alert.account], ['地域', alert.region || '全局'], ['资源类型', kinds[alert.resource_kind] || alert.resource_kind],
    ['对象 ID', alert.subject_id]]
  const scope = alert.budget_scope
  if (scope) fields.push(
    ['账期', scope.period], ['原币种', scope.currency], ['来源', scope.source === 'native' ? '厂商账单' : 'FOCUS 1.0'],
    ['成本口径', scope.basis === 'billed' ? '账单金额' : '摊销成本'],
  )
  if (alert.acknowledged_at) fields.push(['确认人', alert.acknowledged_by || '—'], ['确认时间', timestamp(alert.acknowledged_at)])
  if (alert.resolved_at) fields.push(['恢复时间', timestamp(alert.resolved_at)])

  function acknowledge() {
    void run(() => request(target + '/acknowledgement', { method: 'POST', match: alert.revision }),
      () => { void alertsCollection.refresh(); reload() })
  }

  return <>
    <DetailFields fields={fields} />
    <div className="row-actions">
      {alert.rule.startsWith('operation_') ? <button type="button" onClick={() => showOperation(alert.subject_id, onReload)}>查看关联操作</button> : null}
      {alert.status === 'open' && admin ? <button type="button" disabled={busy} onClick={acknowledge}>确认告警</button> : null}
    </div>
  </>
}
