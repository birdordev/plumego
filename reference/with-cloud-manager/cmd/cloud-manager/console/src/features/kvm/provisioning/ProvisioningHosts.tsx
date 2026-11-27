import { useCallback } from 'react'
import { ConsoleTable } from '../../../shared/components/ConsoleTable'
import { PagedResults } from '../detail/PagedResults'
import { listHosts } from './api'
import type { HostOption } from './schema'

export default function ProvisioningHosts({ base, site, signal, refresh, select }: {
  base: string; site?: string; signal: AbortSignal; refresh: () => void; select: (host: HostOption) => void
}) {
  const read = useCallback((target: string, after: string, readSignal: AbortSignal) => listHosts(target, after, readSignal, site), [site])
  return <>
    <p>选择有有效容量上报的宿主机。预留时会核对实际剩余容量。</p>
    <div className="row-actions"><button type="button" onClick={refresh}>刷新候选宿主机</button></div>
    <PagedResults target={base} read={read} signal={signal}>{(items) => items.length === 0
      ? <p role="status">暂无可提交预留的宿主机。</p>
      : <ConsoleTable columns={['宿主机', '站点', '网络', '存储池']} rows={items.map((item) => ({
        key: item.site + '/' + item.id,
        cells: [<button type="button" onClick={() => select(item)}>{item.id}</button>, item.site,
          item.networks.join('、'), item.shared_storage_pool_id ?? item.storage_pool],
      }))} />}</PagedResults>
  </>
}
