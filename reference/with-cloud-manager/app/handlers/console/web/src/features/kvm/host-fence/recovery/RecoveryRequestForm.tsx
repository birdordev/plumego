import { useRef, useState, type FormEvent } from 'react'
import { setDetailNotice } from '../../../../shared/detail/store'
import { useDetailAction } from '../../../../shared/detail/useDetailAction'
import { evidenceSchema, type HostDetail, type VMDetail } from '../../schema'
import { requestRecovery } from './api'
import { observationLabels, observationSchema, observationsFor, type Recovery } from './schema'

type Props = {
  target: string; host: HostDetail; vm: VMDetail; stamp: number; signal: AbortSignal
  cancel: () => void; onSaved: (receipt: Recovery) => void
}

export function RecoveryRequestForm({ target, host, vm, stamp, signal, cancel, onSaved }: Props) {
  const [observation, setObservation] = useState('')
  const attempt = useRef<{ key: string; id: string }>(undefined)
  const { busy, run } = useDetailAction(stamp)

  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    const fields = new FormData(event.currentTarget)
    const observed = observationSchema.safeParse(observation)
    const first = evidenceSchema.safeParse(fields.get('evidence'))
    if (!observed.success || !observationsFor(vm.state).includes(observed.data) || !first.success || fields.get('verified') !== 'yes') {
      setDetailNotice(stamp, '请核对现场观测和有效的证据摘要。', true)
      return
    }
    let second: string | undefined
    if (observed.data === 'absent') {
      const proof = evidenceSchema.safeParse(fields.get('second'))
      if (!proof.success || proof.data === first.data) {
        setDetailNotice(stamp, '确认域缺失需要两份不同的独立证据摘要。', true)
        return
      }
      second = proof.data
    }
    const key = JSON.stringify([observed.data, first.data, second])
    if (attempt.current?.key !== key) attempt.current = { key, id: crypto.randomUUID() }
    const { id } = attempt.current
    void run(() => requestRecovery(target, vm.revision, host, id, observed.data, first.data, second, signal), onSaved)
  }

  return <form className="catalog-form" aria-label="登记现场恢复证据" onSubmit={submit}>
    <fieldset className="detail-form-fields" disabled={busy}>
      <label>现场观测<select required value={observation} onChange={(event) => setObservation(event.target.value)}>
        <option value="">选择现场观测</option>
        {observationsFor(vm.state).map((value) => <option key={value} value={value}>{observationLabels[value]}</option>)}
      </select></label>
      <label>第一份证据 SHA-256<input name="evidence" required maxLength={64} pattern="[a-f0-9]{64}" /></label>
      {observation === 'absent' && <label>第二份独立证据 SHA-256<input name="second" required maxLength={64} pattern="[a-f0-9]{64}" /></label>}
      <label><input name="verified" type="checkbox" value="yes" required /> 我已核对宿主机隔离、采集的域状态及证据</label>
      <div className="row-actions">
        <button className="primary" type="submit">提交虚拟机恢复申请</button>
        <button type="button" onClick={cancel}>取消</button>
      </div>
    </fieldset>
  </form>
}
