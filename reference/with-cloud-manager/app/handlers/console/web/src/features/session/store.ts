import type { z } from 'zod'
import type { sessionSchema } from './schema'

export type Profile = z.infer<typeof sessionSchema>
export type Project = Profile['projects'][number]
export type Session = Readonly<{ profile: Profile; project: Project }>

let current: Session | null = null
const listeners = new Set<() => void>()

export function sessionSnapshot() {
  return current
}

export function setSession(session: Session | null) {
  if (session === current) return
  current = session
  listeners.forEach((notify) => notify())
}

export function subscribeSession(notify: () => void) {
  listeners.add(notify)
  return () => { listeners.delete(notify) }
}
