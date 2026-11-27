import { useState } from 'react'
import { useDetailAction } from '../../../shared/detail/useDetailAction'
import { addSchedule, changeSchedule, type Schedule } from './api'

const kinds = [
  'region', 'zone', 'instance', 'volume', 'snapshot', 'network', 'subnet', 'image', 'security-group', 'security-group-rule',
  'elastic-ip', 'load-balancer', 'dns-zone', 'object-bucket', 'database-instance', 'cache-instance', 'kubernetes-cluster',
]

export function ScheduleForm({ target, current, readable, stamp, signal, saved, cancel }: {
  target: string; current?: Schedule; readable: boolean; stamp: number; signal: AbortSignal; saved: () => void; cancel: () => void
}) {
  const [kind, setKind] = useState(current?.kind ?? 'instance')
  const [region, setRegion] = useState(current?.region ?? '')
  const [interval, setInterval] = useState(String(current?.interval_seconds ?? 600))
  const [enabled, setEnabled] = useState(current?.enabled ?? readable)
  const { busy, run } = useDetailAction(stamp)
  const global = kind === 'dns-zone' || kind === 'object-bucket'
  const regional = !global && kind !== 'region'
  const valid = Number.isSafeInteger(Number(interval)) && Number(interval) >= 60 && Number(interval) <= 604800 &&
    (!enabled || readable) && (current || !regional || (/^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$/.test(region) && region !== 'global'))

  return <form className="catalog-form" onSubmit={(event) => {
    event.preventDefault()
    if (!valid) return
    const settings = { interval_seconds: Number(interval), enabled }
    void run(() => current ? changeSchedule(target, current, settings, signal)
      : addSchedule(target, { ...settings, kind, region: regional ? region : global ? 'global' : '' }, signal), saved)
  }}>
    {current ? <p>{current.kind} · {current.region || '全局'} · 版本 {current.revision}</p> : <>
      <label>资源类型<select value={kind} onChange={(event) => setKind(event.target.value)}>
        {kinds.map((value) => <option value={value} key={value}>{value}</option>)}
      </select></label>
      {regional && <label>地域<input maxLength={64} required value={region} onChange={(event) => setRegion(event.target.value)} /></label>}
    </>}
    <label>盘点间隔（秒）<input type="number" min={60} max={604800} required value={interval} onChange={(event) => setInterval(event.target.value)} /></label>
    <label><input type="checkbox" checked={enabled} onChange={(event) => setEnabled(event.target.checked)} /> 启用计划</label>
    {!readable && <p>完成云账号身份校验后才可启用计划。</p>}
    <div className="row-actions">
      <button className="primary" disabled={!valid || busy}>保存盘点计划</button>
      <button type="button" disabled={busy} onClick={cancel}>返回计划列表</button>
    </div>
  </form>
}
