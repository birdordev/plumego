import { useState } from 'react'
import { requestErrorMessage } from '../../../../shared/api/requests'
import { useResourceRead } from '../../../../shared/api/useResourceRead'
import { assetsCollection } from '../../inventory/collection'
import type { Asset } from '../../types'
import type { Session } from '../../../session/store'
import { currentReclaimURL, readCurrentReclaim } from './api'
import { ReclaimControls } from './ReclaimControls'
import { ReclaimStatus } from './ReclaimStatus'
import type { Reclaim, TransferredOwnership } from './schema'

type Props = { asset: Asset; project: string; session: Session; stamp: number; signal: AbortSignal; reload: () => void }

export default function ReclaimPanel({ asset, project, session, stamp, signal, reload }: Props) {
  const result = useResourceRead(currentReclaimURL(project, asset), readCurrentReclaim, signal)
  const [saved, setSaved] = useState<Reclaim>()
  const [transfer, setTransfer] = useState<TransferredOwnership>()
  const reclaim = saved ?? (result.state === 'ready' ? result.data.reclaim : null)

  function onTransfer(ownership: TransferredOwnership) {
    setTransfer(ownership)
    void assetsCollection.refresh()
  }

  return <>
    <p>{asset.name || asset.id} · {asset.account} / {asset.region} · <code>{asset.id}</code></p>
    {transfer ? <p role="status">已转移到项目 {transfer.project}，新归属版本 {transfer.revision}。</p> : <>
      <button type="button" onClick={reload}>刷新回收状态</button>
      {result.state === 'loading' ? <p role="status">正在读取回收状态…</p> : null}
      {result.state === 'error' ? <p role="alert">{requestErrorMessage(result.error)}</p> : null}
      {result.state === 'ready' ? <>
        {reclaim ? <ReclaimStatus reclaim={reclaim} /> : <p>当前没有进行中的回收。</p>}
        <ReclaimControls asset={asset} project={project} session={session} stamp={stamp} reclaim={reclaim}
          onSaved={setSaved} onTransfer={onTransfer} />
      </> : null}
    </>}
  </>
}
