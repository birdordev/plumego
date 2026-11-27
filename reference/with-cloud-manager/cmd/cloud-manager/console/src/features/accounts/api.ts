import { request } from '../session/client'
import { accountPage, accountSchema, grantPage, type AccountChange, type AccountInput } from './schema'

function query(after: string) {
  const params = new URLSearchParams({ limit: '20' })
  if (after) params.set('after', after)
  return '?' + params
}

export function listAccounts(target: string, after: string, signal: AbortSignal) {
  return request(target + query(after), { signal }, accountPage)
}

export function listGrants(target: string, after: string, signal: AbortSignal) {
  return request(target + '/grants' + query(after), { signal }, grantPage)
}

export function readAccount(target: string, signal: AbortSignal) {
  return request(target, { signal }, accountSchema)
}

export function registerAccount(target: string, input: AccountInput, signal: AbortSignal) {
  return request(target, { method: 'POST', body: JSON.stringify(input), signal }, accountSchema)
}

export function changeAccount(target: string, revision: number, change: AccountChange, signal: AbortSignal) {
  return request(target, { method: 'PATCH', match: revision, body: JSON.stringify(change), signal }, accountSchema)
}
