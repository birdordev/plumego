import type { FormEvent } from 'react'
import { DetailFields } from '../../../shared/components/DetailFields'
import { setDetailNotice } from '../../../shared/detail/store'
import { useDetailAction } from '../../../shared/detail/useDetailAction'
import { evidenceSchema, siteID, siteIDPattern, type HostDetail } from '../schema'
import { changeHostState, confirmCleanup, type HostAction } from './api'
import type { FenceHistory } from './schema'
import { descriptions, labels } from './state'

export type FenceIntent = { action: HostAction } | { action: 'cleanup'; vm: string; migration: string }

type Props = {
  intent: FenceIntent; history: FenceHistory; target: string; stamp: number; signal: AbortSignal
  cancel: () => void; onSaved: () => void
}

const cleanupInstructions = '请核对源端域与自动启动配置已清除，遗留网络配置已清理，且原域无法再次接入。'

function actionFields(intent: FenceIntent, host: HostDetail, events: FenceHistory['events']) {
  if (intent.action === 'cleanup') return <DetailFields fields={[
    ['宿主机', host.id], ['隔离代次', host.generation], ['虚拟机', intent.vm], ['疏散 ID', intent.migration],
  ]} />
  const event = events.find((candidate) => candidate.revision === host.revision)
  return <DetailFields fields={[
    ['宿主机', host.id], ['UUID', host.uuid], ['版本', host.revision], ['隔离代次', host.generation],
    ...(event?.action === 'request-rejoin' ? [
      ['申请人', event.actor], ['替换代理', event.replacement_agent], ['申请证据 SHA-256', event.evidence_sha256],
    ] as const : []),
  ]} />
}

export function FenceActionForm({ intent, history: { host, events }, target, stamp, signal, cancel, onSaved }: Props) {
  const { busy, run } = useDetailAction(stamp)
  const { action } = intent
  const label = action === 'cleanup' ? '确认疏散源端清理' : labels[action]
  const evidenceRequired = action === 'confirm' || action === 'request-rejoin' || action === 'cleanup'

  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    const fields = new FormData(event.currentTarget)
    const evidence = evidenceSchema.safeParse(fields.get('evidence'))
    const agent = siteID.safeParse(fields.get('agent'))
    if (evidenceRequired && (!evidence.success || fields.get('verified') !== 'yes')) {
      setDetailNotice(stamp, '请核对现场证据并填写 64 位小写 SHA-256 摘要。', true)
      return
    }
    if (action === 'request-rejoin' && (!agent.success || agent.data === host.agent)) {
      setDetailNotice(stamp, '请输入与当前绑定不同的代理服务主体 ID。', true)
      return
    }
    if (intent.action === 'cleanup') {
      if (!evidence.success) return
      void run(() => confirmCleanup(target, host.revision, intent.vm, intent.migration, evidence.data, signal), onSaved)
      return
    }
    const hostAction = intent.action
    void run(() => changeHostState(target, host, hostAction, signal, evidence.data, agent.data), onSaved)
  }

  return <form className="catalog-form" aria-label={label} onSubmit={submit}>
    {actionFields(intent, host, events)}
    <p>{action === 'cleanup' ? cleanupInstructions : descriptions[action]}</p>
    <fieldset className="detail-form-fields" disabled={busy}>
      {action === 'request-rejoin' && <label>替换代理服务主体<input name="agent" required maxLength={64} pattern={siteIDPattern} /></label>}
      {evidenceRequired && <>
        <label>现场证据 SHA-256<input name="evidence" required maxLength={64} pattern="[a-f0-9]{64}" /></label>
        <label><input name="verified" value="yes" type="checkbox" required /> 我已核对现场证据</label>
      </>}
      <div className="row-actions">
        <button className="primary" type="submit">{label.startsWith('确认') ? '提交' + label : '确认' + label}</button>
        <button type="button" autoFocus onClick={cancel}>取消</button>
      </div>
    </fieldset>
  </form>
}
