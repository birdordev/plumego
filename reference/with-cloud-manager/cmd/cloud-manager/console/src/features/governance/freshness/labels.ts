export const freshnessStates = [
  ['fresh', '近期完成'], ['stale', '已过期'], ['pending', '尚无完整结果'],
] as const

export const freshnessStateLabels = new Map<string, string>(freshnessStates)
