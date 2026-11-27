import type { ComponentProps } from 'react'

export type WorkspaceLink<View extends string> = (view: View, filters?: string) =>
  Pick<ComponentProps<'a'>, 'href' | 'onClick'>
