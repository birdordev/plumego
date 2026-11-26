import type { ListAssetActions } from '../assets/row-action'
import { showNativeRequest } from './request'

export const nativeAssetActions: ListAssetActions = (asset, context) => {
  if (!context.canExecute) return []
  if (context.nativeConsole && asset.provider === 'openstack' && /^[a-f0-9]{8}(-[a-f0-9]{4}){3}-[a-f0-9]{12}$/.test(asset.id)) {
    return [{ label: '申请访问', run: () => showNativeRequest(asset, false) }]
  }
  if (context.nativeSerial && asset.provider === 'aws-ec2' && /^i-(?:[a-f0-9]{8}|[a-f0-9]{17})$/.test(asset.id)) {
    return [{ label: '申请访问', run: () => showNativeRequest(asset, true) }]
  }
  return []
}
