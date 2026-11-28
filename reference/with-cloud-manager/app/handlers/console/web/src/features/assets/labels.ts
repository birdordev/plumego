export const kinds: Readonly<Partial<Record<string, string>>> = {
  'billing-budget': '云预算',
  'billing-project-budget': '项目预算',
  instance: '实例',
  'bare-metal': '裸金属服务器',
  volume: '卷',
  snapshot: '云盘快照',
  network: '网络',
  subnet: '子网',
  'security-group': '安全组',
  'security-group-rule': '安全组规则',
  'elastic-ip': '弹性公网 IP',
  'load-balancer': '负载均衡',
  'dns-zone': '公网 DNS 托管区',
  'database-instance': '托管数据库',
  'cache-instance': '托管缓存',
  'kubernetes-cluster': 'Kubernetes 集群',
  'kubernetes-node-pools': 'Kubernetes 节点池',
  'kubernetes-workloads': 'Kubernetes 工作负载',
  'object-bucket': '对象存储桶',
  image: '镜像',
  zone: '可用区',
  region: '地域',
}

export const inventoryStates: Readonly<Partial<Record<string, string>>> = {
  missing_candidate: '待确认缺失',
  stale: '观测已过期',
}

export const resourceStates: Readonly<Partial<Record<string, string>>> = {
  running: '运行中',
  stopped: '已停止',
  active: '运行正常',
  available: '可用',
  creating: '创建中',
  deleting: '删除中',
  error: '异常',
}
