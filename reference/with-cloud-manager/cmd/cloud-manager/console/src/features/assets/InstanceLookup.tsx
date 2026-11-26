import { useEffect, useRef, useState } from 'react'
import { requestErrorMessage } from '../../shared/api/requests'
import { setDetailNotice } from '../../shared/detail/store'
import { request } from '../session/client'
import { projectURL } from '../session/project'
import { assetPageSchema } from './schema'
import type { Asset } from './types'

type Props = {
  asset: Asset
  label: string
  stamp: number
  signal: AbortSignal
  onVerified: (instance: Asset | undefined) => void
}

export function InstanceLookup({ asset, label, stamp, signal, onVerified }: Props) {
  const [value, setValue] = useState('')
  const [verified, setVerified] = useState<Asset>()
  const [busy, setBusy] = useState(false)
  const lookup = useRef<AbortController>(undefined)

  useEffect(() => () => lookup.current?.abort(), [])

  function reset() {
    lookup.current?.abort()
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
      const query = new URLSearchParams({ kind: 'instance', account: asset.account, region: asset.region, q: id, limit: '100' })
      const page = await request(projectURL() + '/assets?' + query, { signal: readSignal }, assetPageSchema)
      if (readSignal.aborted) return
      const instance = page.items.find((item) => item.id === id && item.kind === 'instance' &&
        item.account === asset.account && item.region === asset.region && item.inventory_state === 'observed' && item.ownership_revision > 0)
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
    }
  }

  return <>
    <label>{label}<input name="instance_id" value={value} required maxLength={128} onChange={(event) => {
      setValue(event.currentTarget.value)
      reset()
    }} /></label>
    <button type="button" disabled={busy} onClick={() => void verify()}>核对实例</button>
    {verified ? <p className="hint" role="status">{verified.name || verified.id} · 归属版本 {verified.ownership_revision}</p> : null}
  </>
}
