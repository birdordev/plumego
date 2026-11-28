import { useRef, useState } from 'react'
import { requestErrorMessage } from '../api/requests'
import { isCurrentDetail, setDetailNotice } from './store'

export function useDetailAction(stamp: number) {
  const active = useRef(false)
  const [busy, setBusy] = useState(false)

  async function run<Result>(action: () => Promise<Result>, onSaved?: (result: Result) => void) {
    if (active.current || !isCurrentDetail(stamp)) return
    active.current = true
    setBusy(true)
    setDetailNotice(stamp)

    try {
      const result = await action()
      if (isCurrentDetail(stamp)) onSaved?.(result)
    } catch (error) {
      setDetailNotice(stamp, requestErrorMessage(error), true)
    } finally {
      active.current = false
      if (isCurrentDetail(stamp)) setBusy(false)
    }
  }

  return { busy, run }
}
