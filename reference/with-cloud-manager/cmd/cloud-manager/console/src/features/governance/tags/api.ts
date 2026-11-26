import { request } from '../../session/client'
import { tagPolicySchema, tagReportSchema, type TagPolicyInput } from './schema'

export function readTagPolicy(base: string, signal: AbortSignal) {
  return request(base + '/governance/tag-policy', { signal }, tagPolicySchema)
}

export function saveTagPolicy(base: string, input: TagPolicyInput, revision: number | undefined, signal: AbortSignal) {
  return request(base + '/governance/tag-policy', {
    method: revision === undefined ? 'POST' : 'PUT', match: revision, body: JSON.stringify(input), signal,
  }, tagPolicySchema)
}

export function readTagReport(base: string, query: URLSearchParams, signal: AbortSignal) {
  return request(base + '/governance/tag-compliance?' + query, { signal }, tagReportSchema)
}
