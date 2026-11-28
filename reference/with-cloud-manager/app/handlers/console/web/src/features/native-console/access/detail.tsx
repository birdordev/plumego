import { requestErrorMessage } from '../../../shared/api/requests'
import { DetailFields, type DetailField } from '../../../shared/components/DetailFields'
import { StateBadge } from '../../../shared/components/RowAction'
import { openDetail } from '../../../shared/detail/store'
import { useResourceRead } from '../../../shared/api/useResourceRead'
import { grantPurposes } from '../labels'
import { timestamp } from '../../../shared/display/time'
import { projectURL } from '../../session/project'
import { sessionSnapshot, type Session } from '../../session/store'
import { GrantControls } from './GrantControls'
import { readGrant } from './api'

export function showNativeGrant(id: string) {
  const target = projectURL() + '/console-grants/' + encodeURIComponent(id)
  const session = sessionSnapshot()
  openDetail('原生控制台访问', (stamp, signal) =>
    <GrantDetail target={target} stamp={stamp} signal={signal} session={session} reload={() => showNativeGrant(id)} />)
}

function GrantDetail({ target, stamp, signal, session, reload }:
  { target: string; stamp: number; signal: AbortSignal; session: Session | null; reload: () => void }) {
  const result = useResourceRead(target, readGrant, signal)
  if (result.state === 'loading') return <p role="status">正在加载…</p>
  if (result.state === 'error') return <p role="alert" className="notice" data-error="true">{requestErrorMessage(result.error)}</p>

  const item = result.data
  const state = item.provider === 'aws-ec2' && item.status === 'in_use' ? '正在发放' : <StateBadge status={item.status} />
  const fields: DetailField[] = [['实例', item.asset_id], ['账号 / 地域', item.account + ' / ' + item.region],
    ['用途', grantPurposes[item.purpose] || item.purpose], ['申请人', item.requester], ['审批人', item.reviewer || '—'],
    ['状态', state], ['有效期', timestamp(item.expires_at)]]
  if (item.key_fingerprint) fields.push(['公钥指纹', item.key_fingerprint])
  if (item.provider_request_id) fields.push(['AWS 请求 ID', item.provider_request_id])

  return <>
    <DetailFields fields={fields} />
    <GrantControls item={item} target={target} stamp={stamp} session={session} reload={reload} />
  </>
}
