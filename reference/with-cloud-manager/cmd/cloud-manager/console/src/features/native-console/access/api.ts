import { request } from '../../session/client'
import { grantDetailSchema, grantSchema, serialReceiptSchema } from '../schema'

export function readGrant(target: string, signal: AbortSignal) {
  return request(target, { signal }, grantDetailSchema)
}

export function decideGrant(target: string, revision: number, decision: 'approve' | 'reject') {
  return request(target + '/approval', { method: 'POST', match: revision, body: JSON.stringify({ decision }) }, grantSchema)
}

export function revokeGrant(target: string, revision: number) {
  return request(target + '/revoke', { method: 'POST', match: revision }, grantSchema)
}

export function activateSerialKey(target: string, revision: number, publicKey: string) {
  return request(target + '/serial-key', {
    method: 'POST', match: revision, body: JSON.stringify({ ssh_public_key: publicKey }),
  }, serialReceiptSchema)
}
