import { projectCanExecute, projectPath } from '../../features/session/project'
import type { Session } from '../../features/session/store'
import { WorkspaceNotice } from '../../shared/feedback/WorkspaceNotice'
import type { WorkspaceLink } from '../../shared/navigation/workspace-link'
import type { View } from '../routes/registry'
import { Sidebar } from './Sidebar'
import { useBatchNavigation } from './useBatchNavigation'
import { WorkspaceBatch } from './WorkspaceBatch'
import { WorkspaceHeader } from './WorkspaceHeader'
import { WorkspacePage } from './WorkspacePage'
import type { Theme } from './useWorkspacePreferences'
import { workspaceHref } from './workspace-location'
import { followWorkspaceLink } from './workspace-link'

type Props = {
  session: Session
  view: View
  filters: string
  commitFilters: (filters: string) => string
  ending: boolean
  refreshing: boolean
  refreshCollection?: () => void
  collapsed: boolean
  theme: Theme
  selectTheme: (theme: Theme) => void
  toggleRail: () => void
  selectProject: (id: string) => void
  selectView: (view: View, filters?: string) => void
  expireSession: () => void
  logout: () => Promise<void>
}

export function AuthenticatedWorkspace({
  session, view, filters, commitFilters, ending, refreshing, refreshCollection,
  collapsed, theme, selectTheme, toggleRail, selectProject, selectView, expireSession, logout,
}: Props) {
  const { editor, visited, initialInput, openSelectedBatch } = useBatchNavigation(view, selectView)
  const base = projectPath(session)
  const link: WorkspaceLink<View> = (target, query = '') => ({
    href: workspaceHref(session.project.id, target, query),
    onClick: (event) => followWorkspaceLink(event, () => {
      selectView(target, query)
      document.getElementById('content')?.focus()
    }),
  })

  return (
    <div id="workspace" className="workspace" data-rail-collapsed={collapsed}>
      <a className="skip-link" href="#content">跳到页面内容</a>
      <Sidebar session={session} view={view} filters={filters} collapsed={collapsed} theme={theme}
        toggleRail={toggleRail} onTheme={selectTheme} onView={selectView} />
      <main id="content" tabIndex={-1}>
        <WorkspaceHeader session={session} view={view} filters={filters} ending={ending} refreshing={refreshing}
          onRefresh={refreshCollection} onProject={selectProject} onLogout={logout} />
        <WorkspaceNotice />
        <div className="workspace-page">
          {view === 'batch' ? null : <WorkspacePage view={view} session={session} filters={filters}
            commitFilters={commitFilters} openSelectedBatch={openSelectedBatch} link={link} expireSession={expireSession} />}
          {visited ? <WorkspaceBatch base={base} canExecute={projectCanExecute(session)} active={view === 'batch'}
            initialInput={initialInput} editor={editor} /> : null}
        </div>
      </main>
    </div>
  )
}
