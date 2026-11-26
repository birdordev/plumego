import type { ReactNode } from 'react'

type Detail = {
  stamp: number
  title: string
  content: ReactNode
  notice?: { text: string; error: boolean }
}

let stamp = 0
let current: Detail | null = null
let controller: AbortController | undefined
const listeners = new Set<() => void>()

function publish(detail: Detail | null) {
  current = detail
  listeners.forEach((notify) => notify())
}

export function subscribeDetail(notify: () => void) {
  listeners.add(notify)
  return () => { listeners.delete(notify) }
}

export function detailSnapshot() {
  return current
}

export function openDetail(title: string, render: (stamp: number, signal: AbortSignal) => ReactNode) {
  controller?.abort()
  controller = new AbortController()
  const id = ++stamp
  publish({ stamp: id, title, content: render(id, controller.signal) })
}

export function closeDetail() {
  controller?.abort()
  controller = undefined
  if (current) publish(null)
}

export function isCurrentDetail(id: number) {
  return current?.stamp === id
}

export function setDetailNotice(id: number, text = '', error = false) {
  if (current?.stamp === id) publish({ ...current, notice: text ? { text, error } : undefined })
}
