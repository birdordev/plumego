import { z } from 'zod'
import { time } from '../../../shared/api/fields'
import { request } from '../../session/client'
import { instanceType } from './schema'

const resizeOptions = z.object({
  state: z.enum(['pending', 'ready', 'unavailable']),
  current_type: instanceType,
  ownership_revision: z.number().int().positive(),
  zone: z.string().optional(),
  target_types: z.array(instanceType).max(128),
  observed_at: time.optional(),
  expires_at: time.optional(),
})

function waitForNextRead(signal: AbortSignal) {
  return new Promise<void>((resolve, reject) => {
    signal.throwIfAborted()
    function abort() {
      clearTimeout(timer)
      reject(signal.reason)
    }
    const timer = setTimeout(() => {
      signal.removeEventListener('abort', abort)
      resolve()
    }, 2000)
    signal.addEventListener('abort', abort, { once: true })
  })
}

export async function readInstanceResizeOptions(target: string, signal: AbortSignal) {
  let result = await request(target, { method: 'POST', signal }, resizeOptions)
  for (let attempt = 0; result.state === 'pending' && attempt < 18; attempt += 1) {
    await waitForNextRead(signal)
    result = await request(target, { signal }, resizeOptions)
  }
  return result
}
