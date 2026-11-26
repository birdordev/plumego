import { batchInputSchema } from './schema'

export function batchInputError(payload: string) {
  let data: unknown
  try {
    data = JSON.parse(payload)
  } catch {
    return '请填写有效的 JSON。'
  }
  if (!batchInputSchema.safeParse(data).success) {
    return '请填写 1 至 25 个操作及对应的资产、账号、地域和归属版本。'
  }
  return ''
}
