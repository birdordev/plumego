import { useEffect, useRef, useState } from 'react'
import { requestErrorMessage } from '../../shared/api/requests'
import { request } from '../session/client'
import { setWorkspaceNotice } from '../../shared/feedback/notice'
import { auditCollection } from './collection'

function downloadAudit(blob: Blob) {
  const url = URL.createObjectURL(blob)
  const link = document.createElement('a')
  link.href = url
  link.download = 'project-audit.csv'
  document.body.append(link)
  link.click()
  link.remove()
  setTimeout(() => URL.revokeObjectURL(url), 1000)
}

export function AuditExport() {
  const [downloading, setDownloading] = useState(false)
  const active = useRef<AbortController>(undefined)

  useEffect(() => () => active.current?.abort(), [])

  async function exportAudit() {
    const { scope, filters } = auditCollection.snapshot()
    if (!scope || active.current) return
    const controller = new AbortController()
    const { signal } = controller
    const isCurrent = () => {
      const current = auditCollection.snapshot()
      return !signal.aborted && current.scope === scope && current.filters === filters
    }
    active.current = controller
    setDownloading(true)
    try {
      const blob = await request(scope.base + '/audit/export' + (filters ? '?' + filters : ''), { csv: true, signal })
      if (isCurrent()) downloadAudit(blob)
    } catch (error) {
      if (isCurrent()) setWorkspaceNotice(requestErrorMessage(error), true)
    } finally {
      active.current = undefined
      if (!signal.aborted) setDownloading(false)
    }
  }

  return <button id="audit-export" type="button" disabled={downloading} onClick={() => void exportAudit()}>下载审计 CSV</button>
}
