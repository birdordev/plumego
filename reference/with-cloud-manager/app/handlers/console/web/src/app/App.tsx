import { LoginPage } from '../features/session/login/LoginPage'
import { projectPath } from '../features/session/project'
import { DetailDialog } from '../shared/detail/DetailDialog'
import { AuthenticatedWorkspace } from './workspace/AuthenticatedWorkspace'
import { useConsoleWorkspace } from './workspace/useConsoleWorkspace'
import { useWorkspacePreferences } from './workspace/useWorkspacePreferences'

export function App() {
  const { session, notice, restoring, login, ...workspace } = useConsoleWorkspace()
  const preferences = useWorkspacePreferences()
  return <>
    <LoginPage hidden={Boolean(session)} restoring={restoring || workspace.ending} notice={notice} onLogin={login} />
    {session ? <AuthenticatedWorkspace key={projectPath(session)} session={session} {...workspace} {...preferences} /> : null}
    <DetailDialog />
  </>
}
