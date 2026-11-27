import type { ComponentProps } from 'react'

export type WorkspaceLink<View extends string> = (view: View, filters?: string) =>
  Pick<ComponentProps<'a'>, 'href' | 'onClick'>

export const filterPrefix = 'filter.'

export function workspaceHref(project: string, view: string, filters = '') {
  const url = new URL(window.location.href)
  const query = new URLSearchParams({ project, view })
  for (const [name, value] of new URLSearchParams(filters)) query.set(filterPrefix + name, value)
  url.search = query.toString()
  url.hash = ''
  return url.href
}
