import { openDetail } from '../../../shared/detail/store'
import { OperationForm } from '../../operations/forms/OperationForm'
import { projectURL } from '../../session/project'
import type { Asset } from '../types'

export const unboundElasticIP: Partial<Record<string, string>> = { 'tencent-cvm': 'UNBIND', 'alibaba-ecs': 'Available', 'huawei-ecs': 'DOWN' }
const boundElasticIP: Partial<Record<string, string>> = { 'tencent-cvm': 'BIND', 'alibaba-ecs': 'InUse', 'huawei-ecs': 'ACTIVE' }

export function observedIPv4ElasticIP(asset: Asset) {
  return asset.kind === 'elastic-ip' && asset.inventory_state === 'observed' && asset.ownership_revision > 0 &&
    asset.public_addresses?.length === 1 && /^\d+\.\d+\.\d+\.\d+$/.test(asset.public_addresses[0])
}

export function canReleaseElasticIP(asset: Asset) {
  return observedIPv4ElasticIP(asset) && unboundElasticIP[asset.provider] === asset.status
}

export function canUnbindElasticIP(asset: Asset) {
  return observedIPv4ElasticIP(asset) && boundElasticIP[asset.provider] === asset.status
}

export function showElasticIPRelease(asset: Asset) {
  const address = asset.public_addresses?.[0]
  if (!address) return
  const target = projectURL() + '/network-operations/release-elastic-ip'
  openDetail('申请释放弹性公网 IP', (stamp) => <>
    <p>{asset.account} / {asset.region} · {asset.id} · {address}</p>
    <p className="hint">释放后可能无法取回原地址。提交后由独立审批人审核，执行前还会复核绑定状态。</p>
    <OperationForm stamp={stamp} target={target} revision={asset.ownership_revision} submitLabel="提交释放申请"
      payload={() => ({ account: asset.account, region: asset.region, asset_id: asset.id, public_address: address })} />
  </>)
}
