const outlines = {
  cloud: 'M7 18h11a4 4 0 0 0 .2-8 6.2 6.2 0 0 0-11.8 1.5A3.3 3.3 0 0 0 7 18Z',
  rail: 'M3 5h18v14H3zM9 5v14m8-10-3 3 3 3',
  menu: 'M4 6h16M4 12h16M4 18h16',
  theme: 'M12 3v18a9 9 0 0 0 0-18Zm0 0a9 9 0 0 0 0 18',
  overview: 'M4 4h16v16H4zM4 10h16m-9 0v10m4-4h2',
  assets: 'm12 3 9 5-9 5-9-5 9-5Zm-9 9 9 5 9-5M3 16l9 5 9-5',
  accounts: 'M3 5h18v14H3zM7 15v-1a3 3 0 0 1 6 0v1m-3-4a2 2 0 1 0 0-4 2 2 0 0 0 0 4Zm6-2h2m-2 5h2',
  server: 'M4 4h16v7H4zM4 13h16v7H4zM7 8h1m-1 9h1m4-9h5m-5 9h5',
  catalog: 'M4 5h6v6H4zM14 5h6v6h-6zM4 15h6v5H4zM14 15h6v5h-6z',
  terminal: 'M3 5h18v14H3zM7 9l3 3-3 3m6 0h4',
  activity: 'M3 12h4l3-6 4 12 3-6h4',
  batch: 'M4 7h13v13H4zM8 4h12v12m-2-6H8m0 4h6',
  bell: 'M5 17h14l-2-3v-4a5 5 0 0 0-10 0v4l-2 3Zm5 3h4',
  time: 'M12 3a9 9 0 1 1 0 18 9 9 0 0 1 0-18Zm0 4v5l3 2',
  gauge: 'M4 18v-5a8 8 0 0 1 16 0v5H4Zm5-2 5-6',
  shield: 'M12 3l7 3v5c0 4-3 7-7 10-4-3-7-6-7-10V6l7-3Zm-3 9 2 2 4-4',
  ticket: 'M4 6h16v4a2 2 0 0 0 0 4v4H4v-4a2 2 0 0 0 0-4V6Zm8 3v1m0 4v1',
  audit: 'M6 4h12v16H6zM9 8h6m-6 4h6m-6 4h4',
  bill: 'M6 4h12v16l-3-2-3 2-3-2-3 2V4Zm3 4h6m-6 4h6',
  budget: 'M4 7h16v12H4zM5 7V5h12m-1 7h4',
  allocation: 'M12 3v9h9A9 9 0 0 0 12 3Zm-3 2a8 8 0 1 0 10 10H9V5Z',
  logout: 'M9 5H4v14h5m-1-7h12m-4-4 4 4-4 4',
  copy: 'M9 9h11v11H9zM15 6V4H4v11h2',
  check: 'm5 12 4 4L19 6',
  refresh: 'M20 10a8 8 0 1 0-.9 6m.9-11v5h-5',
  close: 'm6 6 12 12M18 6 6 18',
  filter: 'M4 6h16l-6 7v5l-4 2v-7Z',
} as const

export type ConsoleIconName = keyof typeof outlines

export function ConsoleIcon({ name }: { name: ConsoleIconName }) {
  return <svg className="console-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor"
    strokeWidth="1.65" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
    <path d={outlines[name]} />
  </svg>
}
