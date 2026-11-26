export const reasons: Readonly<Partial<Record<string, string>>> = {
  ready: '可受理',
  stale: '实例版本已变更',
  volume_stale: '卷版本已变更',
  protected: '受删除保护',
  busy: '有活动操作',
  unavailable: '资源不可访问',
  unsupported_parameters: '厂商不支持这些参数',
}
