const bucketSettingKinds: Record<string, string> = {
  'object-bucket-encryption': '对象桶默认加密',
  'object-bucket-versioning': '对象桶版本控制',
}

export const scheduleKinds = [
  'region', 'zone', 'instance', 'volume', 'snapshot', 'network', 'subnet', 'image', 'security-group', 'security-group-rule',
  'elastic-ip', 'load-balancer', 'dns-zone', 'object-bucket', ...Object.keys(bucketSettingKinds),
  'database-instance', 'cache-instance', 'kubernetes-cluster',
]

export function scheduleKindLabel(kind: string) {
  return bucketSettingKinds[kind] ?? kind
}

export function hasGlobalScheduleScope(kind: string) {
  return kind === 'dns-zone' || kind === 'object-bucket' || Object.hasOwn(bucketSettingKinds, kind)
}

export function scheduleRegionLabel(region: string) {
  return region === 'global' || region === '' ? '全局' : region
}
