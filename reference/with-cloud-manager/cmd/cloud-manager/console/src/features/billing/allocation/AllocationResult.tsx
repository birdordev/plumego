import { useState } from 'react'
import { ConsoleTable } from '../../../shared/components/ConsoleTable'
import { DetailFields } from '../../../shared/components/DetailFields'
import { timestamp } from '../../../shared/display/labels'
import type { Allocation } from './schema'

export function AllocationResult({ value }: { value: Allocation }) {
  const currencies = [...new Set(value.totals.map((total) => total.currency))]
  const [selected, setSelected] = useState(currencies[0] ?? '')
  const currency = currencies.includes(selected) ? selected : currencies[0]
  return <>
    <h2>{value.account} · {value.period}</h2>
    <DetailFields fields={[
      ['账单版本', value.report_revision + (value.report_current ? '（当前）' : '（历史）')],
      ['分摊版本', value.revision + (value.current ? '（当前）' : '（历史）')],
      ['账单记录', value.source_rows], ['生成时间', timestamp(value.created_at)],
      ['账单 SHA-256', <code>{value.report_sha256}</code>],
      ['规则 SHA-256', <code>{value.policy_sha256}</code>],
    ]} />
    {value.invalid_tag_rows > 0 ? <p>有 {value.invalid_tag_rows} 条记录的标签格式无法匹配规则。</p> : null}
    <label>查看原币种<select value={currency ?? ''} onChange={(event) => setSelected(event.currentTarget.value)}>
      {currencies.map((item) => <option key={item}>{item}</option>)}
    </select></label>
    <ConsoleTable columns={['成本中心', '账单金额', '摊销成本', '目录价', '目录价与账单差额', '参与行数']}
      rows={value.totals.filter((total) => total.currency === currency).map((total) => ({
        key: total.center, cells: [
          total.center === 'unallocated' ? '未归属' : total.center,
          total.billed_cost, total.effective_cost, total.list_cost, total.list_minus_billed, total.rows,
        ],
      }))} />
  </>
}
