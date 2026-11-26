import { useDetailAction } from '../../shared/detail/useDetailAction'
import { request } from '../session/client'
import type { Session } from '../session/store'
import { grantsCollection } from './collection'
import type { GrantDetail } from './schema'
import { SerialActivationForm } from './SerialActivationForm'

type Props = { item: GrantDetail; target: string; stamp: number; session: Session | null; reload: () => void }

export function GrantControls({ item, target, stamp, session, reload }: Props) {
  const { busy, run } = useDetailAction(stamp)
  const own = item.requester === session?.profile.principal
  const role = session?.project.role
  const revocable = ['awaiting_approval', 'approved'].includes(item.status) || (item.provider === 'openstack' && item.status === 'in_use')
  const novnc = own && item.status === 'approved' && item.provider === 'openstack' && session?.profile.native_console
  const serial = own && item.status === 'approved' && item.provider === 'aws-ec2' && session?.profile.native_serial_console

  function send(suffix: 'approval' | 'revoke', decision?: 'approve' | 'reject') {
    void run(() => request(target + '/' + suffix, { method: 'POST', match: item.revision,
      body: decision ? JSON.stringify({ decision }) : undefined }), () => { void grantsCollection.refresh(); reload() })
  }

  return <>
    <div className="row-actions">
      {item.status === 'awaiting_approval' && role === 'approver' && !own ? <>
        <button type="button" disabled={busy} onClick={() => send('approval', 'approve')}>批准访问</button>
        <button type="button" disabled={busy} onClick={() => send('approval', 'reject')}>拒绝访问</button>
      </> : null}
      {novnc ? <a target="_blank" rel="noopener noreferrer"
        href={'/console-assets/novnc/vnc.html#' + new URLSearchParams({ path: target + '/stream', autoconnect: '1', reconnect: '0' })}>
        打开 noVNC
      </a> : null}
      {(own || role === 'admin') && revocable ? <button type="button" disabled={busy} onClick={() => send('revoke')}>撤销访问</button> : null}
    </div>
    {serial ? <SerialActivationForm target={target} revision={item.revision} stamp={stamp} /> : null}
  </>
}
