import { useState } from 'react'
import { requestErrorMessage } from '../../../shared/api/requests'
import { useResourceRead } from '../../../shared/api/useResourceRead'
import { readInventoryFreshness } from './api'
import { FreshnessFilters } from './FreshnessFilters'
import { FreshnessResults } from './FreshnessResults'

export default function InventoryFreshnessPage({ base }: { base: string }) {
  return <ProjectFreshness key={base} base={base} />
}

function ProjectFreshness({ base }: { base: string }) {
  const [query, setQuery] = useState('')
  const [cursors, setCursors] = useState([''])
  const [revision, setRevision] = useState(0)
  function showFirstPage() {
    setCursors([''])
    setRevision((current) => current + 1)
  }
  const after = cursors[cursors.length - 1]
  const params = new URLSearchParams(query)
  if (after) params.set('after', after)
  const target = base + '/governance/inventory-freshness?' + params
  return <>
    <FreshnessFilters apply={(filters) => { setQuery(filters); showFirstPage() }} />
    <p className="hint">统计启用的全量盘点计划。状态筛选只影响明细，汇总始终包含当前厂商和账号筛选下的所有范围。</p>
    <div className="row-actions"><button type="button" onClick={showFirstPage}>刷新盘点状态</button></div>
    <FreshnessPage key={target + '#' + revision} target={target}
      previous={after ? () => setCursors((current) => current.slice(0, -1)) : undefined}
      advance={(next) => setCursors((current) => [...current, next])} />
  </>
}

function FreshnessPage({ target, previous, advance }: { target: string; previous?: () => void; advance: (next: string) => void }) {
  const result = useResourceRead(target, readInventoryFreshness)
  const report = result.state === 'ready' ? result.data : undefined
  const next = report?.next
  return <>
    {result.state === 'loading' && <p role="status">正在读取盘点状态…</p>}
    {result.state === 'error' && <p role="alert">{requestErrorMessage(result.error)}</p>}
    {report && <FreshnessResults report={report} />}
    {(previous || next) && <div className="row-actions">
      {previous && <button type="button" onClick={previous}>上一页</button>}
      {next && <button type="button" onClick={() => advance(next)}>下一页</button>}
    </div>}
  </>
}
