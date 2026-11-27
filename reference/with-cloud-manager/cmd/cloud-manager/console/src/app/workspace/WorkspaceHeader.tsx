import type { Session } from '../../features/session/store'
import { ConsoleIcon } from '../../shared/components/ConsoleIcon'
import { routes, type View } from '../routes/registry'
import { PageLink } from './PageLink'
import { viewPresentation } from './navigation'
import { workspaceHref } from '../../shared/navigation/workspace-link'

const roleLabel = { viewer: '查看者', operator: '操作员', approver: '审批人', admin: '管理员' } as const

type Props = {
  session: Session
  view: View
  filters: string
  ending: boolean
  refreshing: boolean
  onRefresh?: () => void
  onProject: (id: string) => void
  onLogout: () => Promise<void>
}

export function WorkspaceHeader({ session: { profile, project }, view, filters, ending, refreshing, onRefresh, onProject, onLogout }: Props) {
  const identity = profile.tenant + ' / ' + profile.principal
  const href = workspaceHref(project.id, view, filters)
  return <header className="workspace-header">
    <div className="workspace-heading">
      <div className="workspace-title">
        <h1 id="title">{routes[view].title}</h1>
        <PageLink key={href} href={href} />
      </div>
      <p className="workspace-intro">{viewPresentation[view].intro}</p>
    </div>
    <div className="workspace-toolbar">
      <label className="project-label">
        <span>当前项目</span>
        <select id="project" aria-label="当前项目" value={project.id} onChange={(event) => onProject(event.target.value)}>
          {profile.projects.map((item) => <option key={item.id} value={item.id}>{item.id}</option>)}
        </select>
      </label>
      <div className="workspace-user">
        <p id="identity" className="identity" title={identity}>{identity}</p>
        <span className="workspace-role">{roleLabel[project.role]}</span>
      </div>
      <div className="workspace-actions">
        {onRefresh && <button type="button" className="workspace-refresh" disabled={refreshing} onClick={onRefresh}>
          <ConsoleIcon name="refresh" />{refreshing ? '刷新中…' : '刷新列表'}
        </button>}
        <button type="button" id="logout" className="icon-button" title="退出控制台" aria-label="退出控制台"
          disabled={ending} onClick={() => void onLogout()}><ConsoleIcon name="logout" /></button>
      </div>
    </div>
  </header>
}
