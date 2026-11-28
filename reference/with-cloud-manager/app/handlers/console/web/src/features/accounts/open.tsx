import { lazy, Suspense } from 'react'
import { closeDetail, openDetail } from '../../shared/detail/store'
import { setWorkspaceNotice } from '../../shared/feedback/notice'

const AccountDetails = lazy(() => import('./AccountDetails'))
const CreateAccount = lazy(() => import('./CreateAccount'))

type Context = { target: string; project: string; projects: string[]; onChanged: () => void }

export function openAccount(context: Context, id: string) {
  const refresh = () => openAccount(context, id)
  openDetail('云账号详情', (stamp, signal) => <Suspense fallback={<p role="status">正在加载云账号…</p>}>
    <AccountDetails target={context.target + '/' + encodeURIComponent(id)} stamp={stamp} signal={signal}
      projects={context.projects} refresh={refresh} onSaved={(updated) => {
        context.onChanged()
        if (updated.project === context.project) {
          refresh()
          return
        }
        closeDetail()
        setWorkspaceNotice('账号已转移至项目 ' + updated.project + '。')
      }} />
  </Suspense>)
}

export function openNewAccount(context: Context) {
  openDetail('登记云账号', (stamp, signal) => <Suspense fallback={<p role="status">正在加载登记表单…</p>}>
    <CreateAccount target={context.target} stamp={stamp} signal={signal} onSaved={(account) => {
      context.onChanged()
      openAccount(context, account.id)
    }} />
  </Suspense>)
}
