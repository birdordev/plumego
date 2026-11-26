import { useSyncExternalStore } from 'react'
import type { createCollection } from './store'

export function useCollection<Item>(collection: ReturnType<typeof createCollection<Item>>) {
  const snapshot = useSyncExternalStore(collection.subscribe, collection.snapshot)
  return snapshot.scope ? snapshot : null
}
