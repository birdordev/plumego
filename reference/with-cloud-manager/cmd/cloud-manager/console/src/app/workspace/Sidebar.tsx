import type { Session } from '../../features/session/store'
import { canOpenView, navigation, routes, type View } from '../routes'

type Props = {
  session: Session
  view: View
  ending: boolean
  onProject: (id: string) => void
  onView: (view: View) => void
  onLogout: () => Promise<void>
}

export function Sidebar({ session: { profile, project }, view, ending, onProject, onView, onLogout }: Props) {
  return (
    <aside className="rail">
      <p className="brand"><span className="mark" aria-hidden="true" />云资源管理</p>
      <label className="project-label">
        当前项目
        <select id="project" aria-label="当前项目" value={project.id} onChange={(event) => onProject(event.target.value)}>
          {profile.projects.map((item) => <option key={item.id} value={item.id}>{item.id}</option>)}
        </select>
      </label>
      <nav id="navigation" aria-label="项目功能">
        {navigation.map((name) => (
          <button key={name} type="button" data-view={name} aria-current={name === view ? 'page' : undefined}
            hidden={!canOpenView(name, { profile, project })}
            onClick={() => onView(name)}>{routes[name].title}</button>
        ))}
      </nav>
      <button type="button" id="logout" className="logout" disabled={ending} onClick={() => void onLogout()}>退出控制台</button>
    </aside>
  )
}
