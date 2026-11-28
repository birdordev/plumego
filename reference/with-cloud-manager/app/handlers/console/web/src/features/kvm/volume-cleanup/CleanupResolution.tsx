import { useState, type FormEvent } from 'react'
import { setDetailNotice } from '../../../shared/detail/store'
import { useDetailAction } from '../../../shared/detail/useDetailAction'
import { evidenceSchema } from '../schema'
import { resolveCleanup } from './api'
import type { Cleanup } from './schema'

type Props = {
  target: string; job: Cleanup; stamp: number; signal: AbortSignal
  cancel: () => void; onSaved: (job: Cleanup) => void
}

export function CleanupResolution({ target, job, stamp, signal, cancel, onSaved }: Props) {
  const [observed, setObserved] = useState('')
  const { busy, run } = useDetailAction(stamp)

  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    const fields = new FormData(event.currentTarget)
    const first = evidenceSchema.safeParse(fields.get('evidence'))
    if ((observed !== 'present' && observed !== 'absent') || !first.success || fields.get('verified') !== 'yes') {
      setDetailNotice(stamp, '请核对现场证据并选择卷的实际状态。', true)
      return
    }
    let second: string | undefined
    if (observed === 'absent') {
      const proof = evidenceSchema.safeParse(fields.get('second'))
      if (!proof.success || proof.data === first.data) {
        setDetailNotice(stamp, '确认卷缺失需要两份不同的独立证据摘要。', true)
        return
      }
      second = proof.data
    }
    const proof = { observed, evidence_sha256: first.data, second_evidence_sha256: second } as const
    void run(() => resolveCleanup(target, job.revision, proof, signal), onSaved)
  }

  return <form className="catalog-form" aria-label="按现场证据结算" onSubmit={submit}>
    <p>请在现场核对原宿主机隔离和旧删除进程已经停止。</p>
    <fieldset className="detail-form-fields" disabled={busy}>
      <label>卷状态<select required value={observed} onChange={(event) => setObserved(event.target.value)}>
        <option value="">选择现场状态</option>
        <option value="present">卷仍存在，保留占用</option>
        <option value="absent">卷已缺失，归还保留容量</option>
      </select></label>
      <label>现场证据 SHA-256<input name="evidence" required maxLength={64} pattern="[a-f0-9]{64}" /></label>
      {observed === 'absent' && <label>第二份独立证据 SHA-256<input name="second" required maxLength={64} pattern="[a-f0-9]{64}" /></label>}
      <label><input type="checkbox" name="verified" value="yes" required /> 我已核对隔离、进程和卷的现场证据</label>
      <div className="row-actions">
        <button type="submit" className="primary">确认结算清理</button>
        <button type="button" onClick={cancel}>取消</button>
      </div>
    </fieldset>
  </form>
}
