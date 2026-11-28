import type { z } from 'zod'
import type { auditEventSchema } from './schema'
import { RowAction } from '../../shared/components/RowAction'
import { ConsoleTable } from '../../shared/components/ConsoleTable'
import { actions } from '../operations/labels'
import { alertRules } from '../alerts/labels'
import { auditActions, auditKinds, auditOutcomes } from './labels'
import { grantPurposes } from '../native-console/labels'
import { statuses } from '../../shared/display/status-labels'
import { timestamp } from '../../shared/display/time'
import { showAlert } from '../alerts/detail'
import { showNativeGrant } from '../native-console/access/detail'
import { showOperation } from '../operations/detail/open'

type AuditEvent = z.infer<typeof auditEventSchema>

function AuditSubject({ event, nativeEnabled }: { event: AuditEvent; nativeEnabled: boolean }) {
  const { category, action, subject_id, related_project } = event
  let open: (() => void) | undefined
  if (['operation', 'approval', 'resolution', 'observation'].includes(category)) open = () => showOperation(subject_id)
  else if (category === 'change_ticket' && action === 'record_receipt') open = () => showOperation(subject_id)
  else if (category === 'alert') open = () => showAlert(subject_id)
  else if (category === 'console_access' && nativeEnabled) open = () => showNativeGrant(subject_id)
  return <div>
    {open ? <RowAction onClick={open}>{subject_id}</RowAction> : subject_id}
    {related_project ? <span className="minor">关联项目 {related_project}</span> : null}
  </div>
}

const actionLabels: Partial<Record<string, string>> = { ...statuses, ...actions, ...auditActions }
const outcomes: Partial<Record<string, string>> = { ...statuses, ...alertRules, ...grantPurposes, ...auditOutcomes }

export function AuditTable({ items, nativeEnabled }: { items: readonly AuditEvent[]; nativeEnabled: boolean }) {
  const columns = ['时间', '事件', '动作', '对象', '主体', '结果']
  const rows = items.map((item) => ({
    key: item.id,
    cells: [
      timestamp(item.occurred_at), auditKinds[item.category as keyof typeof auditKinds] || item.category,
      item.category === 'tag_policy' && item.action === 'publish' ? '发布标签规则' : actionLabels[item.action] || item.action, <AuditSubject event={item} nativeEnabled={nativeEnabled} />, item.actor || '系统',
      item.outcome ? outcomes[item.outcome] || item.outcome : '—',
    ],
  }))
  return <ConsoleTable columns={columns} rows={rows} />
}
