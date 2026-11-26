import { useEffect, useState } from 'react'

type Result<Data> = { state: 'loading' } | { state: 'ready'; data: Data } | { state: 'error'; error: unknown }

export function useDetailRead<Data>(target: string, signal: AbortSignal, read: (target: string, signal: AbortSignal) => Promise<Data>) {
  const [result, setResult] = useState<Result<Data>>({ state: 'loading' })
  useEffect(() => {
    const controller = new AbortController()
    const readSignal = AbortSignal.any([signal, controller.signal])
    if (!readSignal.aborted) {
      void read(target, readSignal).then((data) => {
        if (!readSignal.aborted) setResult({ state: 'ready', data })
      }).catch((error: unknown) => {
        if (!readSignal.aborted) setResult({ state: 'error', error })
      })
    }
    return () => controller.abort()
  }, [target, signal, read])
  return result
}
