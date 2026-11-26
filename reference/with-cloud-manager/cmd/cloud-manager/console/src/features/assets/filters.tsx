import { SelectField } from '../../shared/components/SelectField'

const kinds = [
  ['', '全部类型'], ['instance', '实例'], ['volume', '卷'], ['snapshot', '云盘快照'],
  ['network', '网络'], ['subnet', '子网'], ['elastic-ip', '弹性公网 IP'], ['load-balancer', '负载均衡'],
  ['dns-zone', '公网 DNS 托管区'], ['database-instance', '托管数据库'], ['cache-instance', '托管缓存'],
  ['kubernetes-cluster', 'Kubernetes 集群'], ['object-bucket', '对象存储桶'], ['security-group', '安全组'], ['security-group-rule', '安全组规则'],
  ['image', '镜像'],
] as const

export function AssetFilters() {
  return <>
    <label>名称、ID、域名或地址<input name="q" maxLength={256} /></label>
    <label>账号<input name="account" maxLength={128} /></label>
    <SelectField label="类型" name="kind" options={kinds} />
  </>
}
