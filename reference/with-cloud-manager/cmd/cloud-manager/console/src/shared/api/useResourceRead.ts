import { useEffect, useState } from 'react'

type Result<Data> = { state: 'loading' } | { state: 'ready'; data: Data } | { state: 'error'; error: unknown }

export function useResourceRead<Data>(target: string, read: (target: string, signal: AbortSignal) => Promise<Data>, signal?: AbortSignal) {
  const [result, setResult] = useState<Result<Data>>({ state: 'loading' })
  useEffect(() => {
    const controller = new AbortController()
    const readSignal = signal ? AbortSignal.any([signal, controller.signal]) : controller.signal
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
