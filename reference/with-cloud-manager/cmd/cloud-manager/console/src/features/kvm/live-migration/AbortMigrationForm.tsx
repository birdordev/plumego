import { useCallback, type FormEvent } from 'react'
import { requestErrorMessage } from '../../../shared/api/requests'
import { useResourceRead } from '../../../shared/api/useResourceRead'
import { setDetailNotice } from '../../../shared/detail/store'
import { useDetailAction } from '../../../shared/detail/useDetailAction'
import { readHost } from '../api'
import { evidenceSchema } from '../schema'
import { abortMigration } from './api'
import type { Migration } from './schema'

type Props = {
  item: Migration; sitePath: string; target: string; stamp: number; signal: AbortSignal
  cancel: () => void; onSaved: (updated: Migration) => void
}

export function AbortMigrationForm({ item, sitePath, target, stamp, signal, cancel, onSaved }: Props) {
  const { busy, run } = useDetailAction(stamp)
  const readHosts = useCallback((path: string, requestSignal: AbortSignal) => Promise.all([
    readHost(path + '/' + encodeURIComponent(item.source_host), requestSignal),
    readHost(path + '/' + encodeURIComponent(item.target_host), requestSignal),
  ]), [item.source_host, item.target_host])
  const result = useResourceRead(sitePath + '/hosts', readHosts, signal)
  const isolated = result.state === 'ready' && result.data[0].fence_state === 'fenced' &&
    result.data[0].generation > item.source_generation && result.data[1].fence_state === 'fenced' &&
    result.data[1].generation > item.target_generation

  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (!isolated) return
    const evidence = evidenceSchema.safeParse(new FormData(event.currentTarget).get('evidence'))
    if (!evidence.success) {
      setDetailNotice(stamp, '请输入 64 位小写 SHA-256 摘要。', true)
      return
    }
    void run(() => abortMigration(target, evidence.data, item.revision, signal), onSaved)
  }

  return <section aria-label="终止实时迁移">
    {result.state === 'loading' && <p role="status">正在核对两端隔离代次…</p>}
    {result.state === 'error' && <p role="alert" className="notice" data-error="true">{requestErrorMessage(result.error)}</p>}
    {result.state === 'ready' && !isolated && <p role="status">两端都确认进入新的隔离代次后才能终止迁移。</p>}
    {isolated ? <form className="catalog-form" onSubmit={submit}>
      <p>现场证据应确认迁移任务已经结束，目标端没有残留域定义或自动启动路径。</p>
      <fieldset className="detail-form-fields" disabled={busy}>
        <label>现场证据 SHA-256<input name="evidence" required maxLength={64} pattern="[a-f0-9]{64}" /></label>
        <label><input type="checkbox" required /> 我已核对两端隔离与目标清理证据</label>
        <div className="row-actions">
          <button type="submit" className="primary">确认终止迁移</button>
          <button type="button" onClick={cancel}>取消</button>
        </div>
      </fieldset>
    </form> : <button type="button" onClick={cancel}>返回迁移回执</button>}
  </section>
}
