import { useLayoutEffect, useState } from 'react'

export const themeOptions = [
  ['system', '跟随系统'],
  ['light', '浅色'],
  ['dark', '深色'],
] as const

export type Theme = typeof themeOptions[number][0]

function readPreference(key: string) {
  try {
    return localStorage.getItem(key)
  } catch {
    return null
  }
}

function writePreference(key: string, value: string) {
  try {
    localStorage.setItem(key, value)
  } catch {
    // 存储不可用时仍可在当前页面切换。
  }
}

export function useWorkspacePreferences() {
  const [theme, setTheme] = useState<Theme>(() => {
    const stored = readPreference('cloud-manager-theme')
    return stored === 'light' || stored === 'dark' ? stored : 'system'
  })
  const [collapsed, setCollapsed] = useState(() => readPreference('cloud-manager-rail') === 'collapsed')

  useLayoutEffect(() => {
    if (theme === 'system') delete document.documentElement.dataset.theme
    else document.documentElement.dataset.theme = theme
  }, [theme])

  return {
    theme, collapsed,
    selectTheme(value: Theme) {
      setTheme(value)
      writePreference('cloud-manager-theme', value)
    },
    toggleRail() {
      setCollapsed(!collapsed)
      writePreference('cloud-manager-rail', collapsed ? 'expanded' : 'collapsed')
    },
  }
}
