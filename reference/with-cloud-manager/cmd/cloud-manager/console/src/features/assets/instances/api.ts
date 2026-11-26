import { request } from '../../session/client'
import { projectURL } from '../../session/project'
import { assetPageSchema } from '../schema'
import type { Asset } from '../types'

export async function findEditableInstance({ account, region }: Asset, id: string, signal: AbortSignal) {
  const query = new URLSearchParams({ kind: 'instance', account, region, q: id, limit: '100' })
  const page = await request(projectURL() + '/assets?' + query, { signal }, assetPageSchema)

  return page.items.find((item) => item.id === id && item.kind === 'instance' &&
    item.account === account && item.region === region && item.inventory_state === 'observed' && item.ownership_revision > 0)
}
