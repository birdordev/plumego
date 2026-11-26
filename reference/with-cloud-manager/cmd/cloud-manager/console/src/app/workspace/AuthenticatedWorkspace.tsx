import { projectCanExecute, projectPath } from '../../features/session/project'
import type { Session } from '../../features/session/store'
import { WorkspaceNotice } from '../../shared/feedback/WorkspaceNotice'
import { routes, type View } from '../routes/registry'
import { Sidebar } from './Sidebar'
import { useBatchNavigation } from './useBatchNavigation'
import { WorkspaceBatch } from './WorkspaceBatch'
import { WorkspacePage } from './WorkspacePage'

type Props = {
  session: Session
  view: View
  ending: boolean
  selectProject: (id: string) => void
  selectView: (view: View) => void
  logout: () => Promise<void>
}

export function AuthenticatedWorkspace({ session, view, ending, selectProject, selectView, logout }: Props) {
  const { editor, visited, initialInput, navigate, openSelectedBatch } = useBatchNavigation(selectView)
  const base = projectPath(session)
  const { profile } = session

  return (
    <div id="workspace" className="workspace">
      <Sidebar session={session} view={view} ending={ending} onProject={selectProject} onView={navigate} onLogout={logout} />
      <main id="content" tabIndex={-1}>
        <header className="heading">
          <h1 id="title">{routes[view].title}</h1>
          <p id="identity" className="identity">{profile.tenant} / {profile.principal}</p>
        </header>
        <WorkspaceNotice />
        {view === 'batch' ? null : <WorkspacePage view={view} session={session} openSelectedBatch={openSelectedBatch} />}
        {visited ? <WorkspaceBatch base={base} canExecute={projectCanExecute(session)} active={view === 'batch'}
          initialInput={initialInput} editor={editor} /> : null}
      </main>
    </div>
  )
}
