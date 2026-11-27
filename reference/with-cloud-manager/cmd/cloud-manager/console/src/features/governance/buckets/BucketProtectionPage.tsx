import { useState } from 'react'
import { requestErrorMessage } from '../../../shared/api/requests'
import { useResourceRead } from '../../../shared/api/useResourceRead'
import { readBucketProtection } from './api'
import { BucketProtectionFilters } from './BucketProtectionFilters'
import { BucketProtectionResults, type BucketAssetLink } from './BucketProtectionResults'

type Props = { base: string; filters: string; commitFilters: (filters: string) => string; assetLink: BucketAssetLink }

export default function BucketProtectionPage(props: Props) {
  return <ProjectBucketProtection key={JSON.stringify([props.base, props.filters])} {...props} />
}

function ProjectBucketProtection({ base, filters, commitFilters, assetLink }: Props) {
  const [cursors, setCursors] = useState([''])
  const [revision, setRevision] = useState(0)
  const after = cursors[cursors.length - 1]
  const params = new URLSearchParams(filters)
  if (after) params.set('after', after)
  const target = base + '/governance/bucket-protection?' + params

  function reset() {
    setCursors([''])
    setRevision((value) => value + 1)
  }
  return <div className="collection-page">
    <BucketProtectionFilters filters={filters} apply={(input) => { if (commitFilters(input) === filters) reset() }} />
    <p className="hint">关注项只筛选明细。配置缺口按有效观测计算，版本控制未启用包含未开启和已暂停。</p>
    <div className="row-actions"><button type="button" onClick={reset}>刷新报告</button></div>
    <ProtectionPage key={target + '#' + revision} target={target} assetLink={assetLink}
      previous={after ? () => setCursors((current) => current.slice(0, -1)) : undefined}
      advance={(next) => setCursors((current) => [...current, next])} />
  </div>
}

function ProtectionPage({ target, previous, advance, assetLink }: {
  target: string; previous?: () => void; advance: (next: string) => void; assetLink: BucketAssetLink
}) {
  const result = useResourceRead(target, readBucketProtection)
  if (result.state === 'loading') return <p role="status">正在读取对象桶防护…</p>
  const next = result.state === 'ready' ? result.data.next : undefined
  return <>
    {result.state === 'ready'
      ? <BucketProtectionResults report={result.data} assetLink={assetLink} />
      : <p role="alert">{requestErrorMessage(result.error)}</p>}
    {(previous || next) && <div className="row-actions">
      {previous && <button type="button" onClick={previous}>上一页</button>}
      {next && <button type="button" onClick={() => advance(next)}>下一页</button>}
    </div>}
  </>
}
