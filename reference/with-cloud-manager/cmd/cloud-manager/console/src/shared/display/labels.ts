export const kinds: Readonly<Partial<Record<string, string>>> = { "billing-budget": "云预算", instance: "实例", volume: "卷", snapshot: "云盘快照", network: "网络", subnet: "子网", "security-group": "安全组", "security-group-rule": "安全组规则", "elastic-ip": "弹性公网 IP", "load-balancer": "负载均衡", "dns-zone": "公网 DNS 托管区", "database-instance": "托管数据库", "cache-instance": "托管缓存", "kubernetes-cluster": "Kubernetes 集群", "kubernetes-node-pools": "Kubernetes 节点池", "kubernetes-workloads": "Kubernetes 工作负载", "object-bucket": "对象存储桶", image: "镜像", zone: "可用区", region: "地域" };

export const statuses: Readonly<Partial<Record<string, string>>> = { awaiting_approval: "待审批", queued: "已排队", applying: "执行中", observing: "观测中", needs_review: "待复核", succeeded: "成功", failed: "失败",
  approved: "已批准", rejected: "已拒绝", revoked: "已撤销", in_use: "连接中", closed: "已断开", expired: "已过期",
  key_sent: "公钥已发放", dispatch_unknown: "发放结果不确定",
  open: "待确认", acknowledged: "已确认", resolved: "已恢复", failure: "失败", throttled: "限流", stale: "数据过期", warning: "达到预警线", exceeded: "达到预算额度" };

export const grantPurposes: Readonly<Partial<Record<string, string>>> = { troubleshoot: "故障排查", maintenance: "维护", incident: "应急处置" };

export const alertRules: Readonly<Partial<Record<string, string>>> = { operation_review: "操作待复核", operation_delayed: "操作超时", scan_failures: "连续盘点失败", scan_stale: "盘点数据过期", billing_budget_warning: "预算预警", billing_budget_exceeded: "达到预算额度" };

export const actions: Readonly<Partial<Record<string, string>>> = { start: "启动", stop: "停止", reboot: "重启", delete: "删除", create: "创建实例", "create-network": "创建网络",
	"rename-load-balancer": "负载均衡改名",
  "create-dns-record": "添加公网 DNS 记录",
  "delete-dns-record": "删除公网 DNS 记录",
  "scale-workload": "调整工作负载副本",
  "set-dns-record-status": "启停公网 DNS 解析",
  "rename-load-balancer-listener": "负载均衡监听器改名",
  "rename-load-balancer-target-group": "负载均衡后端组改名",
  "set-load-balancer-backend-weight": "修改后端成员权重",
  "detach-load-balancer-backend": "解绑后端成员",
  "attach-load-balancer-backend": "添加后端成员",
  "create-subnet": "创建子网", "delete-network": "删除 VPC", "delete-subnet": "删除子网", "delete-security-group": "删除安全组", "create-snapshot": "创建云盘快照", "delete-snapshot": "删除云盘快照", "delete-image": "删除自有镜像", "create-image": "制作系统盘镜像", "copy-image": "跨地域复制镜像", "restore-snapshot": "从快照恢复云盘", "rollback-snapshot": "回滚原云盘", "create-security-group": "创建安全组", "create-security-group-rule": "创建安全组规则",
  "create-elastic-ip": "申请弹性公网 IP", "release-elastic-ip": "释放弹性公网 IP", "bind-elastic-ip": "绑定弹性公网 IP",
  "unbind-elastic-ip": "解绑弹性公网 IP", "revoke-security-group-rule": "撤销安全组规则", "attach-volume": "挂卷", "detach-volume": "卸卷" };

export const auditKinds: Readonly<Partial<Record<string, string>>> = { account: "云账号", ownership: "资产归属", operation: "操作状态", approval: "审批", resolution: "人工处置", observation: "续查", catalog: "服务目录", alert: "项目告警", billing_budget: "云预算", tag_policy: "标签规则", console_access: "原生控制台" };

export const auditActions: Readonly<Partial<Record<string, string>>> = { register: "登记", rotate: "轮换凭据", suspend: "停用", resume: "恢复", transfer: "转移归属", assign: "分配归属",
  share: "共享", unshare: "取消共享", publish: "发布模板", retire: "停用模板", opened: "告警触发", acknowledged: "确认告警", resolved: "告警恢复", created: "创建", updated: "更新" };

export const catalogFields = { title: "名称", account: "云账号", region: "地域", image_id: "镜像 ID", size: "规格", network_id: "网络或子网 ID", security_group_id: "安全组 ID" };

export const auditOutcomes: Readonly<Partial<Record<string, string>>> = { approve: "批准", reject: "拒绝", restarted: "已恢复观测" };

export const reasons: Readonly<Partial<Record<string, string>>> = { ready: "可受理", stale: "实例版本已变更", volume_stale: "卷版本已变更", protected: "受删除保护",
  busy: "有活动操作", unavailable: "资源不可访问", unsupported_parameters: "厂商不支持这些参数" };

export const inventoryStates: Readonly<Partial<Record<string, string>>> = { missing_candidate: "待确认缺失", stale: "观测已过期" };

export const dateFormat = new Intl.DateTimeFormat("zh-CN", { dateStyle: "short", timeStyle: "short" });

export function timestamp(value?: string) {
  const date = new Date(value ?? "");
  return Number.isNaN(date.valueOf()) ? "—" : dateFormat.format(date);
}
