import { useEffect, useRef, useState, type FormEvent } from 'react'
import { ConsoleRequestError, requestErrorMessage } from '../../../shared/api/requests'
import { DetailFields } from '../../../shared/components/DetailFields'
import { formText } from '../../../shared/detail/form-values'
import { AllocationResult, AllocationTotals } from './AllocationResult'
import { previewAllocation, publishAllocation } from './api'
import { allocationWriteQuerySchema, type Allocation, type AllocationPreview, type AllocationWriteQuery } from './schema'
import './styles.css'

type ReviewedPolicy = { key: AllocationWriteQuery; body: string; result: AllocationPreview }

export function AllocationEditor({ base }: { base: string }) {
  const active = useRef<AbortController>(undefined)
  const [reviewed, setReviewed] = useState<ReviewedPolicy>()
  const [published, setPublished] = useState<Allocation>()
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  useEffect(() => () => active.current?.abort(), [])

  function clearResult() {
    setReviewed(undefined)
    setPublished(undefined)
    setError('')
  }

  async function run(action: (signal: AbortSignal) => Promise<void>) {
    if (active.current) return
    const controller = new AbortController()
    active.current = controller
    setBusy(true)
    setError('')
    try {
      await action(controller.signal)
    } catch (cause) {
      if (controller.signal.aborted) return
      if (cause instanceof ConsoleRequestError && cause.status === 412) {
        setReviewed(undefined)
        setError('账单、分摊版本或规则已变化，请重新预览。')
      } else {
        setError(requestErrorMessage(cause))
      }
    } finally {
      if (active.current === controller) {
        active.current = undefined
        if (!controller.signal.aborted) setBusy(false)
      }
    }
  }

  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    clearResult()
    const form = new FormData(event.currentTarget)
    const key = allocationWriteQuerySchema.safeParse({
      account: formText(form, 'account'), period: formText(form, 'period'), source: formText(form, 'source'),
      revision: formText(form, 'revision'),
    })
    const body = formText(form, 'policy')
    if (!key.success || !body) {
      setError('请填写账号、账期、精确账单版本和分摊规则。')
      return
    }
    void run(async (signal) => {
      const result = await previewAllocation(base, key.data, body, signal)
      if (!signal.aborted) setReviewed({ key: key.data, body, result })
    })
  }

  function publish() {
    if (!reviewed) return
    void run(async (signal) => {
      const receipt = await publishAllocation(base, reviewed.key, reviewed.body, reviewed.result, signal)
      if (!signal.aborted) {
        setPublished(receipt)
        setReviewed(undefined)
      }
    })
  }

  return <>
    <form onChange={clearResult} onSubmit={submit}>
      <fieldset className="panel allocation-editor" disabled={busy}>
        <div className="filters">
          <label>预览云账号<input name="account" maxLength={128} required /></label>
          <label>预览账期<input name="period" type="month" required /></label>
          <label>预览账单来源<select name="source"><option value="native">厂商账单</option><option value="focus-1.0">FOCUS 1.0</option></select></label>
          <label>预览账单版本<input name="revision" inputMode="numeric" pattern="[1-9][0-9]{0,18}" required /></label>
        </div>
        <label>分摊规则 JSON<textarea name="policy" rows={6} maxLength={65536}
          placeholder={'{"default":[{"center":"platform","weight":10000}]}'} required /></label>
        <button type="submit" className="primary">预览分摊</button>
      </fieldset>
    </form>
    {busy ? <p role="status">正在处理分摊…</p> : null}
    {error ? <p role="alert" className="notice" data-error="true">{error}</p> : null}
    {reviewed ? <section aria-label="分摊预览">
      <DetailFields fields={[
        ['账单版本', reviewed.result.report_revision_text + (reviewed.result.report_current ? '（当前）' : '（历史）')],
        ['当前分摊版本', reviewed.result.current_revision_text],
        ['规则 SHA-256', <code>{reviewed.result.policy_sha256}</code>],
        ['账单行数', reviewed.result.source_rows],
        ['无法匹配标签的行数', reviewed.result.invalid_tag_rows],
      ]} />
      <AllocationTotals totals={reviewed.result.totals} />
      <button type="button" className="primary" disabled={busy} onClick={publish}>
        {reviewed.result.matching_revision_text ? '获取原发布回执' : '发布此分摊'}
      </button>
    </section> : null}
    {published ? <section aria-label="分摊发布回执"><p role="status">分摊回执已确认。</p><AllocationResult value={published} /></section> : null}
  </>
}
