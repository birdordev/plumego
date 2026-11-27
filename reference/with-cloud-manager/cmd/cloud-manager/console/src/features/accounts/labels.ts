import type { Account, AccountChange } from './schema'

const commands = new Map([
  ['tencent-cvm', 'verify-tencent'],
  ['alibaba-ecs', 'verify-alibaba'],
  ['huawei-ecs', 'verify-huawei'],
  ['aws-ec2', 'verify-aws'],
  ['openstack', 'verify-openstack'],
  ['vmware-vsphere', 'verify-vsphere'],
  ['redfish', 'verify-redfish'],
])

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
