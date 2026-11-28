import type { MouseEvent } from 'react'

export function followWorkspaceLink(event: MouseEvent<HTMLAnchorElement>, navigate: () => void) {
  if (event.defaultPrevented || event.button !== 0 || event.altKey || event.ctrlKey || event.metaKey || event.shiftKey) return
  event.preventDefault()
  navigate()
}
