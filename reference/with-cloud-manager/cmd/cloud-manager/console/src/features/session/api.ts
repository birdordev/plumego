import { request } from './client'
import { sessionSchema } from './schema'

export function readSession(signal?: AbortSignal) {
  return request('/console/session', { signal }, sessionSchema)
}

export function startSession(tenant: string) {
  return request('/console/login', { method: 'POST', body: JSON.stringify({ tenant }) })
}

export function endSession() {
  return request('/console/logout', { method: 'POST' })
}
