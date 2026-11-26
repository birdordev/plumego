const dateFormat = new Intl.DateTimeFormat('zh-CN', { dateStyle: 'short', timeStyle: 'short' })

export function timestamp(value?: string) {
  const date = new Date(value ?? '')
  return Number.isNaN(date.valueOf()) ? '—' : dateFormat.format(date)
}
