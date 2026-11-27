import { CollectionFilters } from '../../../shared/collection/CollectionFilters'
import { SelectField } from '../../../shared/components/SelectField'
import { dispatchOptions } from './DispatchStatus'

export const providers = [
  ['tencent-cvm', '腾讯云 CVM'], ['alibaba-ecs', '阿里云 ECS'], ['huawei-ecs', '华为云 ECS'],
  ['aws-ec2', 'AWS EC2'], ['openstack', 'OpenStack'], ['vmware-vsphere', 'VMware vSphere'], ['redfish', 'Redfish'],
] as const

export const states = [
  ['fresh', '近期完成'], ['stale', '已过期'], ['pending', '尚无完整结果'],
] as const

export function FreshnessFilters({ apply }: { apply: (filters: string) => void }) {
  return <CollectionFilters onApply={async (filters) => apply(filters)}>
    <SelectField label="云厂商" name="provider" options={[[ '', '全部厂商' ], ...providers]} />
    <label>账号<input name="account" maxLength={128} /></label>
    <SelectField label="新鲜度状态" name="state" options={[[ '', '全部状态' ], ...states]} />
    <SelectField label="调度状态" name="dispatch_state" options={[[ '', '全部调度状态' ], ...dispatchOptions]} />
    <label>新鲜度窗口（秒）<input type="number" name="window_seconds" required min={60} max={604800} defaultValue={900} /></label>
  </CollectionFilters>
}
