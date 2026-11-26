import type { ListAssetActions } from '../row-action'
import { canViewDNSRecords, showDNSRecords } from './records'

export const dnsActions: ListAssetActions = (asset) => canViewDNSRecords(asset)
  ? [{ label: '查看解析记录', run: () => showDNSRecords(asset) }]
  : []
