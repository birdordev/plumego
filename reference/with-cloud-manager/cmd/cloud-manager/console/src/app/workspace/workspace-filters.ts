import type { View } from '../routes/registry'

const fields: Partial<Record<View, readonly string[]>> = {
  assets: ['q', 'account', 'kind'],
  kvm: ['kind', 'site'],
  operations: ['account', 'status'],
  alerts: ['status', 'rule'],
  audit: ['category', 'account', 'subject_id', 'from', 'until'],
  'idle-instances': ['state', 'account', 'region'],
  'inventory-freshness': ['provider', 'account', 'state', 'dispatch_state', 'order', 'window_seconds'],
  'bucket-protection': ['provider', 'account', 'location', 'finding', 'window_seconds'],
}

export const filterPrefix = 'filter.'

export function filterQuery(view: View, query: URLSearchParams, prefix = '') {
  const result = new URLSearchParams()
  for (const field of fields[view] ?? []) {
    const [value, ...duplicates] = query.getAll(prefix + field)
    if (value && !duplicates.length && value.length <= 512 && !value.includes('\0')) result.set(field, value)
  }
  return result.toString()
}
