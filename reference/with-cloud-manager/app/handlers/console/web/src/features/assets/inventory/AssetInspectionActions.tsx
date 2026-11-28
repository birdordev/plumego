import { useEffect, useRef, useState } from 'react'
import { downloadBlob } from '../../../shared/api/download'
import { requestErrorMessage } from '../../../shared/api/requests'
import { setWorkspaceNotice } from '../../../shared/feedback/notice'
import { request } from '../../session/client'
import { assetsCollection } from './collection'
import { showAssetHistory, showAssetTopology } from './inspection/drawers'

export function AssetInspectionActions() {
  const [downloading, setDownloading] = useState(false)
  const active = useRef<AbortController>(undefined)
  useEffect(() => () => active.current?.abort(), [])

  function inspect(show: (base: string, filters: string) => void) {
    const { scope, filters } = assetsCollection.snapshot()
    if (scope) show(scope.base, filters)
  }

  async function exportAssets() {
    const { scope, filters } = assetsCollection.snapshot()
    if (!scope || active.current) return
    const controller = new AbortController()
    const { signal } = controller
    const isCurrent = () => {
      const current = assetsCollection.snapshot()
      return !signal.aborted && current.scope === scope && current.filters === filters
    }
    active.current = controller
    setDownloading(true)
    try {
      const blob = await request(scope.base + '/assets/export' + (filters ? '?' + filters : ''), { csv: true, signal })
      if (isCurrent()) downloadBlob(blob, 'assets.csv')
    } catch (error) {
      if (isCurrent()) setWorkspaceNotice(requestErrorMessage(error), true)
    } finally {
      active.current = undefined
      if (!signal.aborted) setDownloading(false)
    }
  }

  return <>
    <button type="button" onClick={() => inspect(showAssetHistory)}>资产变化记录</button>
    <button type="button" onClick={() => inspect(showAssetTopology)}>资产拓扑</button>
    <button type="button" disabled={downloading} onClick={() => void exportAssets()}>下载资产 CSV</button>
  </>
}
