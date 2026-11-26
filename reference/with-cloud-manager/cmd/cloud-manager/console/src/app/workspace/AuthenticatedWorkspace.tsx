import { lazy, Suspense, useRef, useState } from 'react'
import type { BatchEditor } from '../../features/batches/BatchPage'
import { formatSelectedInstances } from '../../features/batches/selection-format'
import { projectPath } from '../../features/session/project'
import type { Session } from '../../features/session/store'
import { WorkspaceNotice } from '../../shared/feedback/WorkspaceNotice'
import { FeatureBoundary } from '../../shared/components/FeatureBoundary'
import { WorkspacePage } from './WorkspacePage'
import { routes, type View } from '../routes'
import { Sidebar } from './Sidebar'

const BatchPage = lazy(() => import('../../features/batches/BatchPage'))

type Props = {
  session: Session
  view: View
  ending: boolean
  selectProject: (id: string) => void
  selectView: (view: View) => void
  logout: () => Promise<void>
}

export function AuthenticatedWorkspace({ session, view, ending, selectProject, selectView, logout }: Props) {
  const batch = useRef<BatchEditor>(null)
  const [batchVisited, setBatchVisited] = useState(false)
  const [batchInput, setBatchInput] = useState('')
  const base = projectPath(session)
  const { profile, project } = session
  const canExecute = project.role === 'admin' || project.role === 'operator'

  function navigate(next: View) {
    if (next === 'batch') setBatchVisited(true)
    selectView(next)
  }

  function openSelectedBatch() {
    if (batch.current) batch.current.fill()
    else setBatchInput(formatSelectedInstances('start'))
    navigate('batch')
  }

  return (
    <div id="workspace" className="workspace">
      <Sidebar session={session} view={view} ending={ending} onProject={selectProject} onView={navigate} onLogout={logout} />
      <main id="content" tabIndex={-1}>
        <header className="heading">
          <h1 id="title">{routes[view].title}</h1>
          <p id="identity" className="identity">{profile.tenant} / {profile.principal}</p>
        </header>
        <WorkspaceNotice />
        {view !== 'batch' ? <WorkspacePage base={base} view={view} canExecute={canExecute} admin={project.role === 'admin'}
          nativeConsole={Boolean(profile.native_console)} nativeSerial={Boolean(profile.native_serial_console)}
          onBatch={openSelectedBatch} /> : null}
        {batchVisited ? <div hidden={view !== 'batch'}><FeatureBoundary message="无法加载批量操作，请刷新后重试。">
          <Suspense fallback={view === 'batch' ? <p role="status">正在加载批量操作…</p> : null}>
            <BatchPage ref={batch} base={base} canExecute={canExecute} initialInput={batchInput} />
          </Suspense>
        </FeatureBoundary></div> : null}
      </main>
    </div>
  )
}
