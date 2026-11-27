import { useState } from 'react'
import { requestErrorMessage } from '../../shared/api/requests'
import { useResourceRead } from '../../shared/api/useResourceRead'
import { DetailFields } from '../../shared/components/DetailFields'
import { setDetailNotice } from '../../shared/detail/store'
import { AccountChangeForm, type AccountIntent } from './AccountChangeForm'
import { AccountGrants } from './AccountGrants'
import { AccountSchedules } from './schedules/AccountSchedules'
import { readAccount } from './api'
import { providerName } from '../../shared/display/providers'
import { actions, modes, verifyCommand, writableClouds } from './labels'
import type { Account } from './schema'

type Props = { target: string; signal: AbortSignal; stamp: number; projects: string[]; refresh: () => void; onSaved: (account: Account) => void }

export default function AccountDetails(props: Props) {
  const result = useResourceRead(props.target, readAccount, props.signal)
  if (result.state === 'loading') return <p role="status">正在读取云账号…</p>
  if (result.state === 'error') return <>
    <p role="alert">{requestErrorMessage(result.error)}</p>
    <button type="button" onClick={props.refresh}>刷新云账号</button>
  </>
  return <AccountWorkspace {...props} account={result.data} />
}

function AccountWorkspace({ account, ...props }: Props & { account: Account }) {
  const [intent, setIntent] = useState<AccountIntent>()
  const [showSchedules, setShowSchedules] = useState(false)
  const command = verifyCommand(account.provider)
  const canChange = account.mode !== 'revoked'
  function choose(next?: AccountIntent) {
    setDetailNotice(props.stamp)
    setIntent(next)
  }
  if (intent) return <AccountChangeForm {...props} account={account} intent={intent} cancel={() => choose()} />
  if (showSchedules) return <AccountSchedules account={account} target={props.target} stamp={props.stamp} signal={props.signal}
    back={() => setShowSchedules(false)} />
  const options: AccountIntent['action'][] = ['rotate', account.mode === 'suspended' ? 'resume' : 'suspend']
  if (props.projects.length) options.push('share', 'transfer')
  if (writableClouds.has(account.provider)) {
    if (account.mode === 'read_only') options.push('enable-write')
    if (account.mode === 'write_enabled') options.push('disable-write')
  }
  if (account.provider === 'redfish' && account.mode === 'read_only') options.push('enable-bare-metal-power')
  if (account.provider === 'redfish' && account.mode === 'write_enabled') options.push('disable-bare-metal-power')
  return <>
    <DetailFields fields={[
      ['账号', account.id], ['厂商', providerName(account.provider)], ['状态', modes[account.mode]], ['归属项目', account.project],
      ['版本', account.revision],
    ]} />
    {account.mode === 'pending' && command && <p>在管理端运行 <code>cloud-manager {command} TENANT ACCOUNT</code> 校验，完成后刷新此处。</p>}
    <div className="row-actions">
      <button type="button" onClick={props.refresh}>刷新云账号</button>
      {['tencent-cvm', 'alibaba-ecs', 'huawei-ecs'].includes(account.provider) &&
        <button type="button" onClick={() => setShowSchedules(true)}>盘点计划</button>}
      {canChange && options.map((action) => <button type="button" key={action} onClick={() => choose({ action })}>{actions[action]}</button>)}
    </div>
    <AccountGrants target={props.target} signal={props.signal} revoke={canChange ? (project) => choose({ action: 'unshare', project }) : undefined} />
  </>
}
