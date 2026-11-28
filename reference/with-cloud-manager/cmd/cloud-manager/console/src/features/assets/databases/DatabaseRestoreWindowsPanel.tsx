import { useState } from 'react'
import { ConsoleRequestError, requestErrorMessage } from '../../../shared/api/requests'
import { useResourceRead } from '../../../shared/api/useResourceRead'
import { ConsoleTable } from '../../../shared/components/ConsoleTable'
import { DetailFields } from '../../../shared/components/DetailFields'
import { timestamp } from '../../../shared/display/time'
import { request } from '../../session/client'
import type { Asset } from '../types'
import { showDatabaseRecovery } from './recovery'
import { databaseRestoreWindowsSchema } from './schema'
import './restore-windows.css'

type Props = { target: string; asset: Asset; canExecute: boolean; signal: AbortSignal }
const read = (target: string, signal: AbortSignal) => request(target, { signal }, databaseRestoreWindowsSchema)
const formatUTC = (date: string) => date.replace('T', ' ').replace(/Z$/, '')

export default function DatabaseRestoreWindowsPanel(props: Props) {
  const [reload, setReload] = useState(0)
  return <div className="database-restore-windows">
    <RestoreWindows key={reload} {...props} />
    <button type="button" onClick={() => setReload((value) => value + 1)}>重新读取快照</button>
  </div>
}

function RestoreWindows({ target, asset, canExecute, signal }: Props) {
  const result = useResourceRead(target, read, signal)
  if (result.state === 'loading') return <p role="status">正在读取可恢复时间…</p>
  if (result.state === 'error') {
    if (result.error instanceof ConsoleRequestError && result.error.status === 404) {
      return <p role="status">当前没有可用的恢复时间快照。</p>
    }
    return <p role="alert">{requestErrorMessage(result.error)}</p>
  }
  const snapshot = result.data
  return <>
    <DetailFields fields={[
      ['数据库实例', asset.name], ['观测时间', timestamp(snapshot.observed_at)],
      ['厂商报告范围', snapshot.scope === 'source_instance' ? '回档到源实例' : '实例'],
    ]} />
    {snapshot.windows.length ? <ConsoleTable columns={['开始时间（UTC）', '结束时间（UTC）']}
      rows={snapshot.windows.map((window, index) => ({
        key: window.start_at + '/' + window.end_at + '/' + index,
        cells: [
          <time dateTime={window.start_at}>{formatUTC(window.start_at)}</time>,
          <time dateTime={window.end_at}>{formatUTC(window.end_at)}</time>,
        ],
      }))} /> : <p>厂商当前报告无可恢复时间段。</p>}
    {canExecute && asset.ownership_revision > 0 && (asset.provider === 'huawei-ecs' || asset.provider === 'alibaba-ecs') &&
      asset.database_instance?.engine === 'mysql' && snapshot.windows.length > 0
      ? <button type="button" onClick={() => showDatabaseRecovery(asset, snapshot)}>申请恢复到新实例</button>
      : null}
  </>
}
