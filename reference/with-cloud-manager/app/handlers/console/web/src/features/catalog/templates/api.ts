import { request } from '../../session/client'
import { catalogTemplateSchema, catalogVersionSchema } from '../schema'

export function publishTemplate(target: string, body: string, revision?: number) {
  return request(target, {
    method: revision === undefined ? 'POST' : 'PUT', match: revision, body,
  }, catalogTemplateSchema)
}

export function retireTemplate(target: string, revision: number) {
  return request(target, { method: 'DELETE', match: revision }, catalogTemplateSchema)
}

export function readTemplateVersion(target: string, version: string, signal: AbortSignal) {
  return request(target + '/versions/' + encodeURIComponent(version), { signal }, catalogVersionSchema)
}
