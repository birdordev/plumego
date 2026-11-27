import { request } from '../../session/client'
import { rolloutPolicy, type RolloutRule } from './schema'

const path = '/operation-rollout'

export function readRollout(target: string, signal: AbortSignal) {
  return request(target + path, { signal }, rolloutPolicy)
}

export function replaceRollout(target: string, revision: string, rules: RolloutRule[], signal: AbortSignal) {
  return request(target + path, { method: 'PUT', match: revision, body: JSON.stringify({ rules }), signal }, rolloutPolicy)
}
