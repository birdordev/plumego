import { useState } from 'react'
import { useDetailAction } from '../../shared/detail/useDetailAction'
import { changeAccount } from './api'
import { actions } from './labels'
import { credentialPattern, credentialRef, type Account, type AccountChange } from './schema'

export type AccountIntent = { action: AccountChange['action']; project?: string }

const effects: Partial<Record<AccountChange['action'], string>> = {
  rotate: '账号需要重新校验身份。已停用的账号会保持停用。',
  suspend: '停用后停止领取新的盘点计划，旧账号版本不能提交结果。',
  resume: '恢复后进入待身份校验。',
  share: '目标项目可读取继承账号归属的资产。',
  unshare: '目标项目将失去通过本次授权获得的资产访问权限。',
  transfer: '账号归属将转移到目标项目，现有共享授权会被清除。',
  'enable-write': '账号可依照各动作的权限、审批和 Worker 配置受理云端操作。',
  'disable-write': '账号恢复只读。尚未记录派发的旧任务将被拦截，已派发操作仍须核对结果。',
  'enable-bare-metal-power': '允许该账号受理需要独立审批的裸金属电源操作。',
  'disable-bare-metal-power': '账号将恢复为只读。',
}

export function AccountChangeForm({ account, intent, target, projects, stamp, signal, cancel, refresh, onSaved }: {
  account: Account; intent: AccountIntent; target: string; projects: string[]; stamp: number; signal: AbortSignal
  cancel: () => void; refresh: () => void; onSaved: (account: Account) => void
}) {
  const [ref, setRef] = useState('')
  const [project, setProject] = useState(intent.project ?? '')
  const { busy, run } = useDetailAction(stamp)
  const { action } = intent
  const chooseProject = action === 'share' || action === 'transfer'
  const enabled = action === 'rotate' ? credentialRef.safeParse(ref).success : !chooseProject || projects.includes(project)

  return <form className="catalog-form" onSubmit={(event) => {
    event.preventDefault()
    if (!enabled) return
    const change: AccountChange = action === 'rotate' ? { action, credential_ref: ref }
      : action === 'share' || action === 'transfer' || action === 'unshare' ? { action, target_project: project } : { action }
    void run(() => changeAccount(target, account.revision, change, signal), onSaved)
  }}>
    <h3>{actions[action]}</h3>
    <p>{account.id} · 版本 {account.revision}{intent.project && ' · ' + intent.project}</p>
    <p>{effects[action]}</p>
    {action === 'rotate' && <label>新凭据引用<input type="password" autoComplete="off" value={ref} required
      pattern={credentialPattern} maxLength={256} disabled={busy} onChange={(event) => setRef(event.target.value)} /></label>}
    {chooseProject && <label>目标项目<select value={project} required disabled={busy} onChange={(event) => setProject(event.target.value)}>
      <option value="">选择目标项目</option>
      {projects.map((id) => <option key={id} value={id}>{id}</option>)}
    </select></label>}
    <div className="row-actions">
      <button className="primary" disabled={busy || !enabled}>确认{actions[action]}</button>
      <button type="button" disabled={busy} onClick={cancel}>取消</button>
      <button type="button" disabled={busy} onClick={refresh}>重新读取账号</button>
    </div>
  </form>
}
