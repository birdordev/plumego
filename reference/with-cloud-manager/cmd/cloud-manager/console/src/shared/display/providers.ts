export const providerOptions = [
  ['tencent-cvm', '腾讯云 CVM'],
  ['alibaba-ecs', '阿里云 ECS'],
  ['huawei-ecs', '华为云 ECS'],
  ['aws-ec2', 'AWS EC2'],
  ['openstack', 'OpenStack'],
  ['vmware-vsphere', 'VMware vSphere'],
  ['redfish', 'Redfish'],
] as const

const names = new Map<string, string>(providerOptions)

export function providerName(id: string) {
  return names.get(id) ?? id
}
