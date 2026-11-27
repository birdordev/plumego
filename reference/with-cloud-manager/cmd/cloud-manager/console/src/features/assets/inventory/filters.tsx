import { SelectField } from '../../../shared/components/SelectField'

const kinds = [
  ['', '全部类型'], ['instance', '实例'], ['bare-metal', '裸金属服务器'], ['volume', '卷'], ['snapshot', '云盘快照'],
  ['network', '网络'], ['subnet', '子网'], ['elastic-ip', '弹性公网 IP'], ['load-balancer', '负载均衡'],
  ['dns-zone', '公网 DNS 托管区'], ['database-instance', '托管数据库'], ['cache-instance', '托管缓存'],
  ['kubernetes-cluster', 'Kubernetes 集群'], ['object-bucket', '对象存储桶'], ['security-group', '安全组'], ['security-group-rule', '安全组规则'],
  ['image', '镜像'], ['region', '地域 / 数据中心'], ['zone', '可用区'],
] as const

export function AssetFilters({ values }: { values: URLSearchParams }) {
  return <>
    <label>资源搜索<input name="q" defaultValue={values.get('q') ?? ''} maxLength={256} placeholder="名称、ID、域名或地址" /></label>
    <label>账号<input name="account" defaultValue={values.get('account') ?? ''} maxLength={128} /></label>
    <label>地域<input name="region" defaultValue={values.get('region') ?? ''} maxLength={256} /></label>
    <SelectField label="类型" name="kind" initialValue={values.get('kind')} options={kinds} />
  </>
}
