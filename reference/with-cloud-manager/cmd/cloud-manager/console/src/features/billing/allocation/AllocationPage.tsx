import type { FormEvent } from 'react'
import { formText } from '../../../shared/detail/form-values'
import { AllocationResult } from './AllocationResult'
import { allocationQuerySchema } from './schema'
import { useAllocation } from './useAllocation'

export default function AllocationPage({ base }: { base: string }) {
  const { result, load, rejectQuery } = useAllocation(base)

  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    const data = new FormData(event.currentTarget)
    const query = allocationQuerySchema.safeParse({
      account: formText(data, 'account'), period: formText(data, 'period'), source: formText(data, 'source'),
      revision: formText(data, 'revision') || undefined,
      allocation_revision: formText(data, 'allocation_revision') || undefined,
    })
    if (query.success) void load(query.data)
    else rejectQuery()
  }

  return (
    <section aria-labelledby="title" aria-busy={result.state === 'loading'}>
      <form className="filters" onSubmit={submit}>
        <label>云账号<input name="account" required maxLength={128} /></label>
        <label>账期<input name="period" type="month" required placeholder="YYYY-MM" /></label>
        <label>账单来源<select name="source">
          <option value="native">厂商账单</option>
          <option value="focus-1.0">FOCUS 1.0</option>
        </select></label>
        <label>账单版本<input name="revision" inputMode="numeric" pattern="[1-9][0-9]{0,18}" maxLength={19} placeholder="当前版本" /></label>
        <label>分摊版本<input name="allocation_revision" inputMode="numeric" pattern="[1-9][0-9]{0,18}" maxLength={19} placeholder="当前版本" /></label>
        <button type="submit" className="primary">查询分摊</button>
      </form>
      {result.state === 'loading' ? <p role="status">正在读取分摊…</p> : null}
      {result.state === 'error' ? <p role="alert" className="notice" data-error="true">{result.message}</p> : null}
      {result.state === 'ready' ? <AllocationResult value={result.value} /> : null}
    </section>
  )
}
