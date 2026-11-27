const dateFormat = new Intl.DateTimeFormat('zh-CN', { dateStyle: 'short', timeStyle: 'short' })

export function timestamp(value?: string) {
  const date = new Date(value ?? '')
  return Number.isNaN(date.valueOf()) ? '—' : dateFormat.format(date)
}

export function localDateTime(value: string | null) {
  const date = new Date(value ?? '')
  if (!Number.isFinite(date.getTime())) return ''
  const local = new Date(date.getTime() - date.getTimezoneOffset() * 60_000)
  return Number.isFinite(local.getTime()) ? local.toISOString().slice(0, -1) : ''
}
