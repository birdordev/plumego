import { requestErrorMessage } from '../../../shared/api/requests'
import { DetailFields, type DetailField } from '../../../shared/components/DetailFields'
import { StateBadge } from '../../../shared/components/RowAction'
import { useResourceRead } from '../../../shared/api/useResourceRead'
import { actions } from '../labels'
import { request } from '../../session/client'
import { sessionSnapshot } from '../../session/store'
import { ChangeTicketReview } from './review/ChangeTicketReview'
import { OperationDecisions } from './review/OperationDecisions'
import { OperationTimeline } from './events/OperationTimeline'
import { BareMetalDeploymentSection } from './bare-metal-deployment/BareMetalDeploymentSection'
import { assetRenameFields } from './fields/asset-rename'
import { databaseRecoveryFields } from './fields/database-recovery'
import { dnsOperationFields } from './fields/dns'
import { bucketSettingFields } from './fields/bucket-settings'
import { instanceResizeFields } from './fields/instance-resize'
import { imageOperationFields } from './fields/image'
import { loadBalancerOperationFields } from './fields/load-balancer'
import { retirementFields } from './fields/retirement'
import { networkOperationFields } from './fields/network'
import { readOperationEvents } from './events/api'
import { operationDetailSchema } from './schema'
import { workloadScaleFields } from './fields/workload'
import { snapshotOperationFields } from './fields/snapshot'
import { volumeOperationFields } from './fields/volume'

function readDetails(target: string, signal: AbortSignal) {
  return Promise.all([
    request(target, { signal }, operationDetailSchema),
    readOperationEvents(target, signal),
  ])
}

export default function OperationPanel({ project, target, ticketTarget, stamp, signal, reload }:
  { project: string; target: string; ticketTarget: string; stamp: number; signal: AbortSignal; reload: () => void }) {
  const result = useResourceRead(target, readDetails, signal)
  if (result.state === 'error') return <p role="alert" className="notice" data-error="true">{requestErrorMessage(result.error)}</p>
  if (result.state === 'loading') return <p role="status">正在加载…</p>
  const [op, events] = result.data
  const fields: DetailField[] = [
    ['操作 ID', op.id], ['动作', actions[op.action] || op.action], ['状态', <StateBadge status={op.status} />],
    ['资源', op.asset_id || '待分配'], ['厂商请求', op.provider_request_id || '—'],
  ]
  if (op.catalog_template) fields.push(['模板版本', op.catalog_template + ' / ' + op.catalog_version])
  fields.push(...assetRenameFields(op), ...databaseRecoveryFields(op), ...dnsOperationFields(op), ...loadBalancerOperationFields(op), ...networkOperationFields(op),
    ...retirementFields(op), ...instanceResizeFields(op), ...bucketSettingFields(op), ...imageOperationFields(op), ...snapshotOperationFields(op), ...workloadScaleFields(op), ...volumeOperationFields(op))
  const canReview = sessionSnapshot()?.project.role === 'approver'

  return <>
    <DetailFields fields={fields} />
    <BareMetalDeploymentSection operation={op} project={project} stamp={stamp} reload={reload} />
    <OperationTimeline initial={events} target={target} signal={signal} />
    {op.ticket_policy_revision ? <ChangeTicketReview target={ticketTarget} signal={signal} reload={reload}>
      {(ready) => canReview ? <OperationDecisions operation={op} target={target} stamp={stamp}
        approvalReady={ready} onSaved={reload} /> : null}
    </ChangeTicketReview> : canReview ?
      <OperationDecisions operation={op} target={target} stamp={stamp} onSaved={reload} /> : null}
  </>
}
