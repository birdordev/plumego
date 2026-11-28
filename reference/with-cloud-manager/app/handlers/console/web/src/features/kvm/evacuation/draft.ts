import { draftSchema, type Draft } from './schema'

export function draftKey(target: string, principal: string) {
  return 'plumego:kvm:evacuation:v1:' + encodeURIComponent(principal) + ':' + target
}

export function readDraft(key: string): Draft | undefined {
  const saved = sessionStorage.getItem(key)
  if (saved === null) return
  try {
    return draftSchema.parse(JSON.parse(saved))
  } catch {
    throw new Error('保存的疏散申请格式有误。')
  }
}

export function saveDraft(key: string, draft: Draft) {
  const saved = readDraft(key)
  if (saved && saved.id !== draft.id) throw new Error('该宿主机已有本标签页保存的疏散申请。')
  sessionStorage.setItem(key, JSON.stringify(draftSchema.parse(draft)))
}

export function clearDraft(key: string, id: string) {
  if (readDraft(key)?.id === id) sessionStorage.removeItem(key)
}
