import { useState } from 'react'
import { requestErrorMessage } from '../../../shared/api/requests'
import { useResourceRead } from '../../../shared/api/useResourceRead'
import { DatabaseProtectionFilters } from './DatabaseProtectionFilters'
import { DatabaseProtectionResults, type DatabaseAssetLink } from './DatabaseProtectionResults'
import { readDatabaseProtection } from './api'

type Props = { base: string; filters: string; commitFilters: (filters: string) => string; assetLink: DatabaseAssetLink }

export default function DatabaseProtectionPage(props: Props) {
  return <ProjectDatabaseProtection key={JSON.stringify([props.base, props.filters])} {...props} />
}

function ProjectDatabaseProtection({ base, filters, commitFilters, assetLink }: Props) {
  const [cursors, setCursors] = useState([''])
  const [revision, setRevision] = useState(0)
  const after = cursors[cursors.length - 1]
  const params = new URLSearchParams(filters)
  if (after) params.set('after', after)
  const target = base + '/governance/database-protection?' + params

  function reset() {
    setCursors([''])
    setRevision((value) => value + 1)
  }
  return <div className="collection-page">
    <DatabaseProtectionFilters filters={filters} apply={(input) => { if (commitFilters(input) === filters) reset() }} />
    <p className="hint">关注项仅筛选明细。恢复窗口来自云厂商观测，实际恢复仍需核验数据。</p>
    <div className="row-actions"><button type="button" onClick={reset}>刷新报告</button></div>
    <ProtectionPage key={target + '#' + revision} target={target} assetLink={assetLink}
      previous={after ? () => setCursors((current) => current.slice(0, -1)) : undefined}
      advance={(next) => setCursors((current) => [...current, next])} />
  </div>
}

function ProtectionPage({ target, previous, advance, assetLink }: {
  target: string; previous?: () => void; advance: (next: string) => void; assetLink: DatabaseAssetLink
}) {
  const result = useResourceRead(target, readDatabaseProtection)
  if (result.state === 'loading') return <p role="status">正在读取数据库恢复保障…</p>
  const next = result.state === 'ready' ? result.data.next : undefined
  return <>
    {result.state === 'ready'
      ? <DatabaseProtectionResults report={result.data} assetLink={assetLink} />
      : <p role="alert">{requestErrorMessage(result.error)}</p>}
    {(previous || next) && <div className="row-actions">
      {previous && <button type="button" onClick={previous}>上一页</button>}
      {next && <button type="button" onClick={() => advance(next)}>下一页</button>}
    </div>}
  </>
}
