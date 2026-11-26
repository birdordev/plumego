import { useEffect, useRef, useState } from 'react'
import { requestErrorMessage } from '../../shared/api/requests'
import { sessionSnapshot } from '../session/store'

export function useBatchRead(onError: (message: string) => void) {
  const active = useRef<AbortController | null>(null)
  const [reading, setReading] = useState(false)
  useEffect(() => () => active.current?.abort(), [])

  function cancel() {
    active.current?.abort()
    active.current = null
    setReading(false)
  }

  async function read<T>(load: (signal: AbortSignal) => Promise<T>, apply: (result: T) => void) {
    active.current?.abort()
    const controller = new AbortController()
    const session = sessionSnapshot()
    active.current = controller
    setReading(true)

    const isCurrent = () => !controller.signal.aborted && sessionSnapshot() === session
    try {
      const result = await load(controller.signal)
      if (isCurrent()) apply(result)
    } catch (error) {
      if (isCurrent()) onError(requestErrorMessage(error))
    } finally {
      if (isCurrent()) setReading(false)
      if (active.current === controller) active.current = null
    }
  }

  return { cancel, read, reading }
}
