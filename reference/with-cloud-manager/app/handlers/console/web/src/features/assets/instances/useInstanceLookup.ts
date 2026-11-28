import { useEffect, useRef, useState } from 'react'
import { requestErrorMessage } from '../../../shared/api/requests'
import { setDetailNotice } from '../../../shared/detail/store'
import type { Asset } from '../types'
import { findEditableInstance } from './api'

type Options = {
  asset: Asset
  stamp: number
  signal: AbortSignal
  onVerified: (instance: Asset | undefined) => void
}

export function useInstanceLookup({ asset, stamp, signal, onVerified }: Options) {
  const [value, setValue] = useState('')
  const [verified, setVerified] = useState<Asset>()
  const [busy, setBusy] = useState(false)
  const lookup = useRef<AbortController>(undefined)

  useEffect(() => () => lookup.current?.abort(), [])

  function reset() {
    lookup.current?.abort()
    lookup.current = undefined
    setBusy(false)
    setVerified(undefined)
    onVerified(undefined)
    setDetailNotice(stamp)
  }

  async function verify() {
    reset()
    const id = value.trim()
    if (!id) {
      setDetailNotice(stamp, '请填写实例 ID。', true)
      return
    }

    const controller = new AbortController()
    const readSignal = AbortSignal.any([signal, controller.signal])
    lookup.current = controller
    setBusy(true)
    try {
      const instance = await findEditableInstance(asset, id, readSignal)
      if (readSignal.aborted) return
      if (!instance) {
        setDetailNotice(stamp, '当前项目和账号中找不到可操作的实例。', true)
        return
      }
      setVerified(instance)
      onVerified(instance)
    } catch (error) {
      if (!readSignal.aborted) setDetailNotice(stamp, requestErrorMessage(error), true)
    } finally {
      if (!readSignal.aborted) setBusy(false)
      if (lookup.current === controller) lookup.current = undefined
    }
  }

  function edit(next: string) {
    setValue(next)
    reset()
  }

  return { value, verified, busy, edit, verify }
}
