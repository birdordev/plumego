import { useState } from 'react'
import { requestErrorMessage } from '../../../shared/api/requests'
import { useResourceRead } from '../../../shared/api/useResourceRead'
import { setDetailNotice } from '../../../shared/detail/store'
import { providerName } from '../../../shared/display/providers'
import { modes } from '../labels'
import type { Account } from '../schema'
import { readRollout } from './api'
import { RolloutEditor } from './RolloutEditor'
import './styles.css'

type Props = { account: Account; target: string; signal: AbortSignal; stamp: number; back: () => void }

export function AccountRollout(props: Props) {
  const [load, setLoad] = useState(0)
  return <section className="account-rollout" aria-labelledby="rollout-heading">
    <h3 id="rollout-heading">操作放量策略</h3>
    <p>{providerName(props.account.provider)} / {props.account.id} / {modes[props.account.mode]}</p>
    <RolloutContent key={load} {...props} reload={() => {
      setDetailNotice(props.stamp)
      setLoad((value) => value + 1)
    }} />
  </section>
}

function RolloutContent({ target, signal, stamp, back, reload }: Props & { reload: () => void }) {
  const result = useResourceRead(target, readRollout, signal)
  if (result.state === 'ready') {
    return <RolloutEditor initial={result.data} target={target} signal={signal} stamp={stamp} back={back} reload={reload} />
  }
  return <>
    {result.state === 'loading'
      ? <p role="status">正在读取放量策略…</p>
      : <p role="alert">{requestErrorMessage(result.error)}</p>}
    <div className="row-actions">
      <button type="button" onClick={back}>返回云账号</button>
      {result.state === 'error' && <button type="button" onClick={reload}>重新读取策略</button>}
    </div>
  </>
}
