import { useSyncExternalStore } from 'react'
import { subscribeWorkspaceNotice, workspaceNotice } from './notice'

export function WorkspaceNotice() {
  const { text, error } = useSyncExternalStore(subscribeWorkspaceNotice, workspaceNotice)
  return <p id="workspace-message" className="notice" role="status" data-error={error}>{text}</p>
}
