import type { Account, AccountChange } from './schema'

export const providers = [
  ['tencent-cvm', '腾讯云 CVM', 'verify-tencent'],
  ['alibaba-ecs', '阿里云 ECS', 'verify-alibaba'],
  ['huawei-ecs', '华为云 ECS', 'verify-huawei'],
  ['aws-ec2', 'AWS EC2', 'verify-aws'],
  ['openstack', 'OpenStack', 'verify-openstack'],
  ['vmware-vsphere', 'VMware vSphere', 'verify-vsphere'],
  ['redfish', 'Redfish', 'verify-redfish'],
] as const
const names = new Map<string, string>(providers.map(([id, name]) => [id, name]))
const commands = new Map<string, string>(providers.map(([id, , command]) => [id, command]))

export const providerName = (id: string) => names.get(id) ?? id
export const verifyCommand = (id: string) => commands.get(id)
export const writableClouds = new Set<string>(['tencent-cvm', 'alibaba-ecs', 'huawei-ecs', 'aws-ec2', 'openstack'])

export const modes: Record<Account['mode'], string> = {
  pending: '待身份校验', read_only: '只读', write_enabled: '已启用写入', suspended: '已停用', revoked: '已撤销',
}

export const actions: Record<AccountChange['action'], string> = {
  rotate: '轮换凭据引用', suspend: '停用账号', resume: '恢复账号', share: '添加只读共享', unshare: '撤销共享',
  'enable-write': '启用云端写入', 'disable-write': '关闭云端写入',
  transfer: '转移账号归属', 'enable-bare-metal-power': '启用裸金属电源操作', 'disable-bare-metal-power': '关闭裸金属电源操作',
}
