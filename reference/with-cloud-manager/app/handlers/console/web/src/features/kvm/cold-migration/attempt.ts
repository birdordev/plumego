import { attemptSchema, type Attempt } from './schema'

export function migrationKey(target: string, principal: string) {
  return 'plumego:kvm:cold-migration:v1:' + encodeURIComponent(principal) + ':' + target
}

export function readAttempt(key: string): Attempt | undefined {
  const stored = sessionStorage.getItem(key)
  if (stored === null) return
  try {
    return attemptSchema.parse(JSON.parse(stored))
  } catch {
    throw new Error('保存的冷迁移申请格式有误。')
  }
}

export function saveAttempt(key: string, attempt: Attempt) {
  const previous = readAttempt(key)
  if (previous && previous.id !== attempt.id) throw new Error('该虚拟机已有本标签页保存的申请。')
  sessionStorage.setItem(key, JSON.stringify(attempt))
}

export function clearAttempt(key: string, id: string) {
  if (readAttempt(key)?.id === id) sessionStorage.removeItem(key)
}
