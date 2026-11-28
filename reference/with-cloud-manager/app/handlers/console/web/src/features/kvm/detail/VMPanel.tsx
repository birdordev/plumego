import { useState } from 'react'
import { requestErrorMessage } from '../../../shared/api/requests'
import { useResourceRead } from '../../../shared/api/useResourceRead'
import { DetailFields } from '../../../shared/components/DetailFields'
import { setDetailNotice } from '../../../shared/detail/store'
import { useDetailAction } from '../../../shared/detail/useDetailAction'
import type { Project } from '../../session/store'
import { changeVM, readVM, type VMAction } from '../api'
import { openColdMigrations } from '../cold-migration/open'
import { vmStatus } from '../labels'
import { openLiveMigrations } from '../live-migration/open'
import type { VMDetail } from '../schema'
import { openVMVolumes } from '../volume-cleanup/open'
import { capacityFields } from './capacity'
import { actionDescription, actionLabels, availableActions } from './vm-actions'

type Props = {
  base: string; target: string; site: string; sitePath: string; role: Project['role']; stamp: number; signal: AbortSignal
  reload: () => void; onChanged: () => Promise<void>; back?: () => void
}

export default function VMPanel({ base, target, site, sitePath, role, stamp, signal, reload, onChanged, back }: Props) {
  const result = useResourceRead(target, readVM, signal)
  const [saved, setSaved] = useState<VMDetail>()
  const [action, setAction] = useState<VMAction>()
  const { busy, run } = useDetailAction(stamp)
  if (result.state === 'loading') return <p role="status">正在加载虚拟机…</p>
  if (result.state === 'error') return <>
    <p role="alert" className="notice" data-error="true">{requestErrorMessage(result.error)}</p>
    <button type="button" onClick={reload}>重试</button>
  </>
  const vm = saved ?? result.data

  function confirm(next: VMAction) {
    setAction(undefined)
    void run(() => changeVM(target, next, vm.revision, signal), (updated) => {
      setSaved(updated)
      setDetailNotice(stamp, '当前状态为' + vmStatus(updated.state) + '。')
      void onChanged()
    })
  }

  return <>
    <DetailFields fields={[
      ['虚拟机', vm.id], ['站点', site], ['宿主机', vm.host], ['状态', vmStatus(vm.state)], ['版本', vm.revision],
      ['放置 ID', vm.placement_id], ['代理代次', vm.generation], ['最近观测', vm.observation],
      ['网络', vm.network], ['IPv4', vm.ipv4], ['MAC', vm.mac],
      ['本地存储池', vm.storage_pool], ['共享存储池', vm.shared_storage_pool_id], ['启动卷', vm.boot_volume], ['镜像', vm.image_id],
      ...capacityFields(vm.capacity),
    ]} />
    {action ? <section aria-label="确认 KVM 操作">
      <p>{actionDescription(action, vm.state)}</p>
      <div className="row-actions">
        <button className="primary" type="button" onClick={() => confirm(action)}>确认{actionLabels[action]}</button>
        <button type="button" autoFocus onClick={() => setAction(undefined)}>取消</button>
      </div>
    </section> : <div className="row-actions">
      {availableActions(role, vm.state).map((next) => <button type="button" key={next} disabled={busy}
        onClick={() => { setDetailNotice(stamp); setAction(next) }}>{actionLabels[next]}</button>)}
      <button type="button" disabled={busy} onClick={() => openLiveMigrations({
        target, sitePath, vm, role, backToVM: () => { void onChanged(); reload() },
      })}>实时迁移</button>
      <button type="button" disabled={busy} onClick={() => openColdMigrations({ base, target, site, sitePath, role,
        backToVM: () => { void onChanged(); reload() },
      })}>冷迁移与单机疏散</button>
      <button type="button" disabled={busy} onClick={() => openVMVolumes({ target, sitePath, role, back: reload })}>保留卷与清理历史</button>
      <button type="button" disabled={busy} onClick={reload}>刷新详情</button>
      {back && <button type="button" disabled={busy} onClick={back}>返回宿主机隔离</button>}
    </div>}
  </>
}
