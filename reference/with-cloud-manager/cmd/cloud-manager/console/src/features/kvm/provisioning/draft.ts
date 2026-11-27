import { draftSchema, type Draft } from './schema'

export function draftKey(base: string, principal: string) {
  return 'plumego:kvm:provisioning:v1:' + encodeURIComponent(principal) + ':' + base
}

export function readDraft(storageKey: string): Draft | undefined {
  const saved = sessionStorage.getItem(storageKey)
  if (saved === null) return
  try {
    return draftSchema.parse(JSON.parse(saved))
  } catch {
    throw new Error('保存的 KVM 申请格式有误。')
  }
}

export function saveDraft(storageKey: string, draft: Draft) {
  const saved = readDraft(storageKey)
  if (saved && saved.key !== draft.key) throw new Error('当前项目已有待处理的创建申请。请重新打开创建入口。')
  sessionStorage.setItem(storageKey, JSON.stringify(draft))
}

export function clearDraft(storageKey: string, key: string) {
  if (readDraft(storageKey)?.key === key) sessionStorage.removeItem(storageKey)
}
