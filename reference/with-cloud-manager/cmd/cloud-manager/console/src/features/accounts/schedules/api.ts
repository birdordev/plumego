import { z } from 'zod'
import { revision, time } from '../../../shared/api/fields'
import { pageSchema } from '../../../shared/api/page'
import { request } from '../../session/client'

const collection = '/inventory-schedules'
const schedule = z.object({
  id: z.string().regex(/^[1-9][0-9]*$/), region: z.string(), kind: z.string().min(1), revision,
  interval_seconds: z.int().min(60).max(604800), enabled: z.boolean(), next_at: time, failures: z.int().nonnegative(),
  last_outcome: z.enum(['success', 'failure', 'throttled']).optional(), last_finished_at: time.optional(),
})
const page = pageSchema(schedule)
export type Schedule = z.infer<typeof schedule>
export type Settings = { interval_seconds: number; enabled: boolean; run_now?: boolean }
export type NewSchedule = Settings & { kind: string; region: string }

export function listSchedules(target: string, after: string, signal: AbortSignal) {
  const params = new URLSearchParams({ limit: '20' })
  if (after) params.set('after', after)
  return request(target + collection + '?' + params, { signal }, page)
}

export function addSchedule(target: string, input: NewSchedule, signal: AbortSignal) {
  return request(target + collection, { method: 'POST', body: JSON.stringify(input), signal }, schedule)
}

export function changeSchedule(target: string, current: Schedule, input: Settings, signal: AbortSignal) {
  return request(target + collection + '/' + encodeURIComponent(current.id),
    { method: 'PATCH', match: current.revision, body: JSON.stringify(input), signal }, schedule)
}
