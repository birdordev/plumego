import { useRef, useState, type KeyboardEvent, type MouseEvent } from 'react'
import type { Session } from '../../features/session/store'
import { ConsoleBrand } from '../../shared/components/ConsoleBrand'
import { ConsoleIcon } from '../../shared/components/ConsoleIcon'
import { canOpenView, routes, type View } from '../routes/registry'
import { navigationGroups, viewPresentation } from './navigation'
import { themeOptions, type Theme } from './useWorkspacePreferences'
import { workspaceHref } from './workspace-location'
import { followWorkspaceLink } from './workspace-link'

type Props = {
  session: Session
  view: View
  filters: string
  collapsed: boolean
  theme: Theme
  toggleRail: () => void
  onTheme: (theme: Theme) => void
  onView: (view: View) => void
}

export function Sidebar({ session, view, filters, collapsed, theme, toggleRail, onTheme, onView }: Props) {
  const [mobileOpen, setMobileOpen] = useState(false)
  const mobileToggle = useRef<HTMLButtonElement>(null)
  const railLabel = collapsed ? '展开侧边栏' : '折叠侧边栏'
  const menuLabel = mobileOpen ? '收起导航' : '展开导航'

  function followLink(event: MouseEvent<HTMLAnchorElement>, next: View) {
    followWorkspaceLink(event, () => {
      onView(next)
      if (mobileOpen) {
        setMobileOpen(false)
        document.getElementById('content')?.focus()
      }
    })
  }

  function closeMobileNavigation(event: KeyboardEvent<HTMLElement>) {
    if (event.key !== 'Escape' || !mobileOpen || !mobileToggle.current?.getClientRects().length) return
    setMobileOpen(false)
    mobileToggle.current.focus()
  }

  return (
    <aside className="rail" data-mobile-open={mobileOpen} onKeyDown={closeMobileNavigation}>
      <div className="rail-header">
        <ConsoleBrand />
        <button type="button" className="rail-toggle" aria-label={railLabel} title={railLabel}
          aria-expanded={!collapsed} aria-controls="navigation" onClick={toggleRail}>
          <ConsoleIcon name="rail" />
        </button>
        <button ref={mobileToggle} type="button" className="mobile-toggle" aria-label={menuLabel} title={menuLabel}
          aria-expanded={mobileOpen} aria-controls="navigation" onClick={() => setMobileOpen(!mobileOpen)}>
          <ConsoleIcon name="menu" />
        </button>
      </div>
      <div className="theme-control">
        <ConsoleIcon name="theme" />
        <select aria-label="主题模式" title="主题模式" value={theme}
          onChange={(event) => onTheme(themeOptions.find(([value]) => value === event.target.value)?.[0] ?? 'system')}>
          {themeOptions.map(([value, label]) => <option key={value} value={value}>{label}</option>)}
        </select>
      </div>
      <nav id="navigation" aria-label="项目功能">
        {navigationGroups.map((group) => {
          const { views } = group
          const label = 'label' in group ? group.label : undefined
          const visible = views.filter((name) => canOpenView(name, session))
          return visible.length ? <div className="rail-group" role={label ? 'group' : undefined} aria-label={label} key={views[0]}>
            {label && <p className="rail-group-label" aria-hidden="true">{label}</p>}
            {visible.map((name) => (
              <a key={name} href={workspaceHref(session.project.id, name, name === view ? filters : '')} data-view={name} className="rail-link"
                title={collapsed ? routes[name].title : undefined}
                aria-label={routes[name].title} aria-current={name === view ? 'page' : undefined}
                onClick={(event) => followLink(event, name)}>
                <ConsoleIcon name={viewPresentation[name].icon} />
                <span className="rail-link-label">{routes[name].title}</span>
              </a>
            ))}
          </div> : null
        })}
      </nav>
    </aside>
  )
}
