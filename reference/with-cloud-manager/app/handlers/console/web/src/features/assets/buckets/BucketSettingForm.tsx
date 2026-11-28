import { bucketVersioningLabels } from '../../../shared/display/bucket-settings'
import { OperationForm } from '../../operations/forms/OperationForm'

type BucketVersioning = keyof typeof bucketVersioningLabels

export type BucketWriteTarget = {
  url: string
  account: string
  id: string
  revision: number
}

export function VersioningChangeForm({ mutation, current, stamp }:
  { mutation: BucketWriteTarget; current: BucketVersioning; stamp: number }) {
  const target = current === 'enabled' ? 'suspended' : 'enabled'
  const action = current === 'enabled' ? '暂停' : current === 'suspended' ? '恢复' : '启用'
  return <OperationForm stamp={stamp} target={mutation.url} revision={mutation.revision} submitLabel={'申请' + action + '版本控制'}
    payload={() => ({ account: mutation.account, asset_id: mutation.id, current_state: current, target_state: target })}>
    <p>变更为{bucketVersioningLabels[target]}，需独立审批。</p>
    <p className="hint">{target === 'suspended'
      ? '暂停会改变后续对象写入的版本行为。'
      : '开启后，后续对象的多个版本可能产生额外存储费用。'}</p>
  </OperationForm>
}


export function EncryptionEnableForm({ mutation, stamp }: { mutation: BucketWriteTarget; stamp: number }) {
  return <OperationForm stamp={stamp} target={mutation.url} revision={mutation.revision} submitLabel="申请启用默认加密"
    payload={() => ({ account: mutation.account, asset_id: mutation.id })}>
    <p>启用厂商托管的 AES256 默认加密，需独立审批。</p>
    <p className="hint">默认加密作用于后续上传。已有对象的加密状态保持原样。</p>
  </OperationForm>
}
