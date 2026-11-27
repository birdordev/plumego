import { pageSchema } from '../../../shared/api/page'
import { request } from '../../session/client'
import { migrationSchema, targetSchema } from './schema'

const migrationPage = pageSchema(migrationSchema)
const targetPage = pageSchema(targetSchema)

function pageQuery(after: string) {
  const query = new URLSearchParams({ limit: '15' })
  if (after) query.set('after', after)
  return '?' + query
}

export function listMigrations(vm: string, after: string, signal: AbortSignal) {
  return request(vm + '/live-migrations' + pageQuery(after), { signal }, migrationPage)
}

export function listTargets(vm: string, after: string, signal: AbortSignal) {
  return request(vm + '/live-migration-targets' + pageQuery(after), { signal }, targetPage)
}

export function readMigration(path: string, signal: AbortSignal) {
  return request(path, { signal }, migrationSchema)
}

export function requestMigration(vm: string, id: string, host: string, match: number, signal: AbortSignal) {
  const body = JSON.stringify({ migration_id: id, target_host: host })
  return request(vm + '/live-migrations', { method: 'POST', body, match, signal }, migrationSchema)
}

export function reviewMigration(path: string, decision: 'approve' | 'reject', vmRevision: number, signal: AbortSignal) {
  const body = JSON.stringify({ decision })
  return request(path + '/review', { method: 'POST', body, match: vmRevision, signal }, migrationSchema)
}

export function abortMigration(path: string, evidence_sha256: string, migrationRevision: number, signal: AbortSignal) {
  const body = JSON.stringify({ evidence_sha256 })
  return request(path + '/abort', { method: 'POST', body, match: migrationRevision, signal }, migrationSchema)
}
