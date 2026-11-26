import type { Asset } from '../types'
import { useInstanceLookup } from './useInstanceLookup'

type Props = {
  asset: Asset
  label: string
  stamp: number
  signal: AbortSignal
  onVerified: (instance: Asset | undefined) => void
}

export function InstanceLookup({ label, ...options }: Props) {
  const { value, verified, busy, edit, verify } = useInstanceLookup(options)
  return <>
    <label>{label}<input name="instance_id" value={value} required maxLength={128}
      onChange={(event) => edit(event.currentTarget.value)} /></label>
    <button type="button" disabled={busy} onClick={() => void verify()}>核对实例</button>
    {verified ? <p className="hint" role="status">{verified.name || verified.id} · 归属版本 {verified.ownership_revision}</p> : null}
  </>
}
