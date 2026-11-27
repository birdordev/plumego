import { useState } from 'react'
import { ConsoleRequestError } from '../../../shared/api/requests'
import { DetailFields } from '../../../shared/components/DetailFields'
import { useDetailAction } from '../../../shared/detail/useDetailAction'
import { releasePlacement, reservePlacement } from '../provisioning/api'
import { submitColdMigration } from './api'
import { clearAttempt, saveAttempt } from './attempt'
import type { Attempt, Receipt } from './schema'

type Props = {
  target: string; sitePath: string; keyName: string; attempt: Attempt; stamp: number; signal: AbortSignal
  back: () => void; onSaved: (receipt: Receipt) => void
}

export default function MigrationSubmission({ target, sitePath, keyName, attempt, stamp, signal, back, onSaved }: Props) {
  const [current, setCurrent] = useState(attempt)
  const [failed, setFailed] = useState(false)
  const { busy, run } = useDetailAction(stamp)
  const { source, host, image, verified } = current

  function confirm() {
    void run(async () => {
      let request = { ...current, started: true }
      saveAttempt(keyName, request)
      setCurrent(request)
      setFailed(false)
      try {
        if (host && !request.placement) {
          const hold = await reservePlacement(sitePath, request.id, {
            ...source.vm.capacity, network: source.vm.network, required_host: host.id, storage_pool: host.storage_pool,
            shared_storage_pool_id: host.shared_storage_pool_id, ttl_seconds: 900,
            ipv4: source.mode === 'cold' ? source.vm.ipv4 : undefined,
            evacuation_vm_id: source.mode === 'evacuation' ? source.vm.id : undefined,
          }, signal)
          request = { ...request, placement: hold.id }
          saveAttempt(keyName, request)
          setCurrent(request)
          if (hold.host !== host.id || hold.site !== host.site || (hold.ipv4 ?? '') !== (source.mode === 'cold' ? source.vm.ipv4 ?? '' : '')) {
            setFailed(true)
            throw new Error('目标预留与所选宿主机或原 IPv4 不符。')
          }
        }
        const receipt = await submitColdMigration(target, request, signal)
        clearAttempt(keyName, request.id)
        return receipt
      } catch (error) {
        if (error instanceof ConsoleRequestError && [400, 409, 412, 413, 415].includes(error.status)) setFailed(true)
        throw error
      }
    }, onSaved)
  }

  return <>
    <DetailFields fields={[
      ['虚拟机', source.vm.id], ['原 VM 版本', source.vm.revision], ['源宿主机', source.vm.host],
      ['源隔离代次', source.source_fence_generation], ['目标宿主机', host?.id ?? '其他故障域自动选址'],
      ['原 IPv4', source.vm.ipv4], ['备份镜像', image], ['校验备份回执', verified ? '要求有效回执' : '由站点核对'],
      ['目标预留', current.placement],
    ]} />
    <div className="row-actions">
      <button type="button" disabled={busy} className="primary" onClick={confirm}>确认{source.mode === 'cold' ? '申请冷迁移' : '申请单机疏散'}</button>
      <button type="button" disabled={busy} onClick={back}>返回迁移记录</button>
      {(!current.started || failed) && <button type="button" disabled={busy} onClick={() => void run(async () => {
        if (current.placement) await releasePlacement(sitePath + '/placements/' + encodeURIComponent(current.placement), signal)
        clearAttempt(keyName, attempt.id)
      }, back)}>{current.placement ? '结束失败的申请并释放预留' : '放弃迁移草稿'}</button>}
    </div>
  </>
}
