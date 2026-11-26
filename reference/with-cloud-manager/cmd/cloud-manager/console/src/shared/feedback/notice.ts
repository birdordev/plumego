const listeners = new Set<() => void>()
let notice = { text: '', error: false }

export function setWorkspaceNotice(text = '', error = false) {
  if (notice.text === text && notice.error === error) return
  notice = { text, error }
  listeners.forEach((notify) => notify())
}

export function workspaceNotice() {
  return notice
}

export function subscribeWorkspaceNotice(notify: () => void) {
  listeners.add(notify)
  return () => { listeners.delete(notify) }
}
