import { useEffect, useRef, useState } from 'react'
import { ConsoleRequestError } from '../../../shared/api/requests'
import { ConsoleTable } from '../../../shared/components/ConsoleTable'
import { DetailFields } from '../../../shared/components/DetailFields'
import { setDetailNotice } from '../../../shared/detail/store'
import { useDetailAction } from '../../../shared/detail/useDetailAction'
import { replaceRollout } from './api'
import { rolloutRuleKey, type RolloutPolicy, type RolloutRule } from './schema'
import { RolloutRuleForm } from './RolloutRuleForm'

type Props = {
  initial: RolloutPolicy; target: string; signal: AbortSignal; stamp: number; back: () => void; reload: () => void
}

export function RolloutEditor({ initial, target, signal, stamp, back, reload }: Props) {
  const [policy, setPolicy] = useState(initial)
  const [rules, setRules] = useState(initial.rules)
  const [stale, setStale] = useState(false)
  const [confirmFreeze, setConfirmFreeze] = useState(false)
  const cancelFreeze = useRef<HTMLButtonElement>(null)
  const { busy, run } = useDetailAction(stamp)
  const disabled = busy || stale
  const frozen = policy.revision !== '0' && policy.rules.length === 0
  const active = new Set(policy.rules.map(rolloutRuleKey))
  const changed = active.size !== rules.length || rules.some((rule) => !active.has(rolloutRuleKey(rule)))

  useEffect(() => {
    if (confirmFreeze) cancelFreeze.current?.focus()
  }, [confirmFreeze])

  function replace(next: RolloutRule[]) {
    void run(async () => {
      try {
        return await replaceRollout(target, policy.revision, next, signal)
      } catch (error) {
        if (error instanceof ConsoleRequestError && error.status === 412) {
          setStale(true)
          throw new Error('放量策略已变更。请重新读取当前版本，再确认需要提交的规则。')
        }
        throw error
      }
    }, (updated) => {
      setPolicy(updated)
      setRules(updated.rules)
      setConfirmFreeze(false)
      setDetailNotice(stamp, updated.rules.length === 0 ? '该账号的云端派发已冻结。' : '放量策略已保存。')
    })
  }

  function edit(next: RolloutRule[]) {
    setDetailNotice(stamp)
    setRules(next)
  }

  return <>
    <DetailFields fields={[
      ['当前策略', policy.revision === '0' ? '沿用账号写入规则' : frozen ? '已冻结' : '按白名单放量'],
      ['生效版本', policy.revision],
      ['生效规则', policy.rules.length],
    ]} />
    {confirmFreeze ? <section className="catalog-form" aria-labelledby="rollout-freeze-heading" aria-busy={busy}>
      <h4 id="rollout-freeze-heading">确认冻结云端派发</h4>
      <p>冻结会清空该账号的白名单，阻止新的受理与尚未记录派发的操作。已派发的操作继续核对结果。</p>
      <div className="row-actions">
        <button type="button" className="primary" disabled={disabled} onClick={() => replace([])}>确认冻结</button>
        <button ref={cancelFreeze} type="button" disabled={busy} onClick={() => setConfirmFreeze(false)}>取消冻结</button>
      </div>
    </section> : <section className="catalog-form" aria-labelledby="rollout-rules-heading" aria-busy={busy}>
      <h4 id="rollout-rules-heading">{changed ? '地域和动作白名单草稿' : '地域和动作白名单'}</h4>
      <p>每条规则允许一个确切的地域和动作。保存新版本后，旧版本中尚未记录派发的任务需要重新申请。</p>
      {rules.length > 0 && <ConsoleTable columns={['地域', '动作代码', '操作']} rows={rules.map((rule) => ({
        key: rolloutRuleKey(rule),
        cells: [rule.region, rule.action, <button type="button" disabled={disabled}
          aria-label={`移除 ${rule.region} ${rule.action}`} onClick={() => edit(rules.filter((item) => item !== rule))}>移除</button>],
      }))} />}
      <RolloutRuleForm key={policy.revision} rules={rules} disabled={disabled} add={(rule) => edit([...rules, rule])} />
      <div className="row-actions">
        {rules.length > 0 && <button type="button" className="primary" disabled={disabled || !changed}
          onClick={() => replace(rules)}>保存放量策略</button>}
        {!frozen && <button type="button" disabled={disabled} onClick={() => {
          setDetailNotice(stamp)
          setConfirmFreeze(true)
        }}>冻结写入</button>}
      </div>
    </section>}
    <div className="row-actions">
      <button type="button" disabled={busy} onClick={back}>返回云账号</button>
      <button type="button" disabled={busy} onClick={reload}>{changed ? '放弃草稿并重新读取' : '重新读取策略'}</button>
    </div>
  </>
}
