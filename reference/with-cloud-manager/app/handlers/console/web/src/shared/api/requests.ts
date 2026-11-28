import { z } from 'zod'

const messages: Record<number, string> = {
  400: '输入有误。请检查字段。', 401: '会话已失效。请重新登录。',
  403: '当前身份无权执行此操作。', 409: '操作发生冲突。请刷新状态。',
  412: '资源版本已变更。请刷新后重新预览。', 413: '请求过大。请缩小批次。',
  422: '导出超过 2000 条。请缩小筛选范围。', 429: '项目剩余配额不足。',
}

export class ConsoleRequestError extends Error {
  constructor(readonly status: number, message: string) {
    super(message)
  }
}

type RequestOptions = {
  method?: string; body?: string; key?: string; match?: string | number
  csv?: boolean; signal?: AbortSignal
}

const envelope = z.object({ data: z.unknown() }).refine((result) => result.data !== undefined)

export function createRequester(getCSRF: () => string | undefined) {
  function request<T>(path: string, options: RequestOptions, schema: z.ZodType<T>): Promise<T>
  function request(path: string, options: RequestOptions & { csv: true }): Promise<Blob>
  function request(path: string, options?: RequestOptions): Promise<unknown>
  function request(path: string, options: RequestOptions = {}, schema?: z.ZodType) {
    return send(path, options, schema, getCSRF)
  }
  return request
}

async function send(path: string, options: RequestOptions, schema: z.ZodType | undefined, getCSRF: () => string | undefined) {
  const { method = 'GET', body, key, match, csv, signal } = options
  if (!path.startsWith('/') || new URL(path, location.origin).origin !== location.origin) {
    throw new Error('请求地址无效。')
  }
  const headers = new Headers({ Accept: csv ? 'text/csv' : 'application/json' })
  if (body !== undefined) headers.set('Content-Type', 'application/json')
  if (method !== 'GET' && method !== 'HEAD') {
    const csrf = getCSRF()
    if (csrf) headers.set('X-Console-CSRF', csrf)
  }
  if (key) headers.set('Idempotency-Key', key)
  if (match !== undefined) headers.set('If-Match', '"' + match + '"')

  let response: Response
  try {
    response = await fetch(path, { method, body, headers, signal, credentials: 'same-origin', redirect: 'error', cache: 'no-store' })
  } catch (error) {
    if (signal?.aborted) throw error
    throw new Error('连接已中断。请检查网络后重试。')
  }
  if (!response.ok) {
    throw new ConsoleRequestError(response.status, messages[response.status] ?? '暂时无法读取服务。请稍后重试。')
  }
  if (csv) {
    if (response.headers.get('Content-Type')?.split(';')[0] !== 'text/csv') throw new Error('无法读取导出的 CSV 文件。')
    return response.blob()
  }
  const payload = envelope.safeParse(await response.json().catch(() => undefined))
  if (!payload.success) throw new Error('无法读取服务返回的数据。')
  if (!schema) return payload.data.data
  const parsed = schema.safeParse(payload.data.data)
  if (!parsed.success) throw new Error('服务返回的数据格式有误。')
  return parsed.data
}

export function requestErrorMessage(error: unknown) {
  return error instanceof Error ? error.message : '暂时无法完成请求。请稍后重试。'
}
