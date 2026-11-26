export type LaunchAttempt = {
  target: string
  project: string
  body: string
  name: string
  key: string
  lookup: string
  running: boolean
  uncertain: boolean
}

const attempts = new Map<string, LaunchAttempt>()
const listeners = new Set<() => void>()

export function pendingCatalogLaunch(project: string) {
  return Array.from(attempts.values()).find((attempt) => attempt.project === project && attempt.uncertain)
}

export function catalogAttempt(project: string, target: string, body: string, name: string) {
  const lookup = target + ' ' + body
  const previous = attempts.get(lookup)
  if (previous) return previous
  const attempt = { target, project, body, name, lookup, key: crypto.randomUUID(), running: false, uncertain: false }
  attempts.set(lookup, attempt)
  return attempt
}

export function removeCatalogAttempt(attempt: LaunchAttempt) {
  if (attempts.get(attempt.lookup) === attempt) attempts.delete(attempt.lookup)
}

export function clearCatalogAttempts() {
  attempts.clear()
  notifyCatalogRetry()
}

export function subscribeCatalogRetry(notify: () => void) {
  listeners.add(notify)
  return () => { listeners.delete(notify) }
}

export function notifyCatalogRetry() {
  listeners.forEach((notify) => notify())
}
