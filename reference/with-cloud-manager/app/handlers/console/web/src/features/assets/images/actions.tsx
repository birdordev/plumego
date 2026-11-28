import { formText } from '../../../shared/detail/form-values'
import { openDetail } from '../../../shared/detail/store'
import { OperationForm } from '../../operations/forms/OperationForm'
import { projectURL } from '../../session/project'
import type { Asset } from '../types'

const namePattern = '[a-zA-Z][a-zA-Z0-9_\\-]{1,59}'
const stoppedImageSources: Partial<Record<string, string>> = { 'tencent-cvm': 'STOPPED', 'alibaba-ecs': 'Stopped', 'huawei-ecs': 'SHUTOFF' }
const deletableImageStates: Partial<Record<string, string>> = { 'tencent-cvm': 'NORMAL', 'alibaba-ecs': 'Available', 'huawei-ecs': 'active' }

export function canCreateImage(asset: Asset) {
  return asset.kind === 'instance' && asset.inventory_state === 'observed' && asset.ownership_revision > 0 &&
    Object.hasOwn(stoppedImageSources, asset.provider) && stoppedImageSources[asset.provider] === asset.status
}

export function canDeleteImage(asset: Asset) {
  return asset.kind === 'image' && asset.inventory_state === 'observed' && asset.ownership_revision > 0 &&
    deletableImageStates[asset.provider] === asset.status && Object.hasOwn(deletableImageStates, asset.provider)
}

export function showImageCreation(asset: Asset) {
  const target = projectURL() + '/images'
  openDetail('制作系统盘镜像', (stamp) => <>
    <p>{asset.account} / {asset.region} · {asset.id} · 归属版本 {asset.ownership_revision}</p>
    <p className="hint">源实例须保持关机。申请需独立审批。阿里云源实例须只挂载系统云盘。</p>
    <OperationForm stamp={stamp} target={target} revision={asset.ownership_revision} submitLabel="提交镜像制作申请"
      payload={(data) => ({ account: asset.account, region: asset.region, instance_id: asset.id, name: formText(data, 'name').trim() })}>
      <label>镜像名称<input name="name" required maxLength={60} pattern={namePattern} /></label>
    </OperationForm>
  </>)
}

export function showImageCopy(asset: Asset) {
  const target = projectURL() + '/images/copy'
  const needsTargetAccount = asset.provider === 'huawei-ecs'
  openDetail('申请跨地域复制镜像', (stamp) => <>
    <p>{asset.account} / {asset.region} · {asset.id} · 归属版本 {asset.ownership_revision}</p>
    <p className="hint">目标账号和源镜像均须归当前项目，提交后需独立审批。</p>
    <OperationForm stamp={stamp} target={target} revision={asset.ownership_revision} submitLabel="提交镜像复制申请"
      payload={(data) => {
        const region = formText(data, 'destination_region').trim()
        if (region === asset.region) throw new Error('请选择与源镜像不同的地域。')
        return { account: asset.account, region: asset.region, asset_id: asset.id, name: formText(data, 'name').trim(),
          destination_region: region, destination_account: needsTargetAccount ? formText(data, 'destination_account').trim() : asset.account }
      }}>
      <label>目标镜像名称<input name="name" required maxLength={60} pattern={namePattern} /></label>
      <label>目标地域<input name="destination_region" required maxLength={40} /></label>
      {needsTargetAccount ? <label>目标云账号<input name="destination_account" required maxLength={128} /></label> : null}
    </OperationForm>
  </>)
}

export function showImageDeletion(asset: Asset) {
  const target = projectURL() + '/images/delete'
  openDetail('申请删除自有镜像', (stamp) => <>
    <p>{asset.name || asset.id}</p><p>{asset.account} / {asset.region} · {asset.id} · 归属版本 {asset.ownership_revision}</p>
    <p className="hint">删除后无法再使用该镜像创建实例。提交后需独立审批，关联快照和备份会保留。</p>
    <OperationForm stamp={stamp} target={target} revision={asset.ownership_revision} submitLabel="提交镜像删除申请"
      payload={() => ({ account: asset.account, region: asset.region, asset_id: asset.id })} />
  </>)
}
