import type { Profile, Session } from '../../features/session/store'
import { canOpenView, routes, type View } from '../routes/registry'
import { filterPrefix, filterQuery } from './workspace-filters'

export type HistoryAction = 'pushState' | 'replaceState'
export const defaultView: View = 'overview'

export function readWorkspaceLocation(profile: Profile) {
  const query = new URLSearchParams(window.location.search)
  const projectID = query.get('project')
  const requested = projectID === null ? profile.projects[0] : profile.projects.find((project) => project.id === projectID)
  const project = requested ?? profile.projects[0]
  if (!project) return

  const scope = { profile, project }
  const route = query.get('view')
  const view = requested && route && Object.hasOwn(routes, route) && canOpenView(route as View, scope)
    ? route as View : undefined
  const fallback = !requested || route !== null ? 'assets' : defaultView
  return { scope, view: view ?? fallback, filters: view ? filterQuery(view, query, filterPrefix) : '' }
}

export function workspaceHref(project: string, view: View, filters = '') {
  const url = new URL(window.location.href)
  const query = new URLSearchParams({ project, view })
  for (const [name, value] of new URLSearchParams(filters)) query.set(filterPrefix + name, value)
  url.search = query.toString()
  url.hash = ''
  return url.href
}

export function updateWorkspaceLocation(project: string, view: View, filters: string, action: HistoryAction) {
  const target = workspaceHref(project, view, filters) + (action === 'replaceState' ? window.location.hash : '')
  if (target !== window.location.href) window.history[action](null, '', target)
}

export function clearWorkspaceLocation() {
  const url = new URL(window.location.href)
  url.search = ''
  url.hash = ''
  if (url.href !== window.location.href) window.history.replaceState(null, '', url.href)
}
