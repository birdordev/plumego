import { request } from '../../session/client'
import { grantSchema } from '../schema'

export function requestGrant(target: string, revision: number, key: string, body: string) {
  return request(target, { method: 'POST', key, match: revision, body }, grantSchema)
}
