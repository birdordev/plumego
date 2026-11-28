import { useState, type FormEvent } from 'react'
import { ConsoleRequestError, requestErrorMessage } from '../../../shared/api/requests'
import { useResourceRead } from '../../../shared/api/useResourceRead'
import { DetailFields } from '../../../shared/components/DetailFields'
import { openDetail, setDetailNotice } from '../../../shared/detail/store'
import { useDetailAction } from '../../../shared/detail/useDetailAction'
import { projectPath } from '../../session/project'
import { sessionSnapshot } from '../../session/store'
import { readOperationBudget, saveOperationBudget, type OperationBudget } from './api'

export function OperationBudgetButton() {
  function open() {
    const session = sessionSnapshot()
    if (!session) return
    const target = projectPath(session) + '/operation-budget'
    openDetail('项目操作配额', (stamp, signal) => <BudgetDrawer key={stamp}
      target={target} admin={session.project.role === 'admin'} stamp={stamp} signal={signal} />)
  }
  return <button type="button" onClick={open}>查看项目配额</button>
}

type Props = { target: string; admin: boolean; stamp: number; signal: AbortSignal }

function BudgetDrawer(props: Props) {
  const [refresh, setRefresh] = useState(0)
  function reload() {
    setDetailNotice(props.stamp)
    setRefresh((current) => current + 1)
  }
  return <BudgetContent key={refresh} {...props} reload={reload} />
}

function BudgetContent(props: Props & { reload: () => void }) {
  const result = useResourceRead(props.target, readOperationBudget, props.signal)
  if (result.state === 'loading') return <p role="status">正在读取项目配额…</p>
  if (result.state === 'error') return <>
    <p className="notice" role="alert" data-error="true">{requestErrorMessage(result.error)}</p>
    <button type="button" onClick={props.reload}>重试</button>
  </>
  return <BudgetEditor {...props} initial={result.data} />
}

function BudgetEditor({ initial, target, admin, stamp, signal, reload }: Props & {
  initial: OperationBudget; reload: () => void
}) {
  const [budget, setBudget] = useState(initial)
  const { busy, run } = useDetailAction(stamp)

  function save(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    const form = new FormData(event.currentTarget)
    const input = {
      max_active: Number(form.get('max_active')),
      max_managed_instances: Number(form.get('max_managed_instances')),
    }
    void run(async () => {
      try {
        return await saveOperationBudget(target, budget.revision, input, signal)
      } catch (error) {
        if (error instanceof ConsoleRequestError && error.status === 412) {
          throw new Error('配额版本已变化。请重新读取，再核对并提交额度。')
        }
        throw error
      }
    }, (saved) => {
      setBudget(saved)
      setDetailNotice(stamp, '已保存项目配额。')
    })
  }

  return <>
    <DetailFields fields={[
      ['活跃操作占用 / 上限', `${budget.active_operations} / ${budget.max_active}`],
      ['实例预留占用 / 上限', `${budget.reserved_instances} / ${budget.max_managed_instances}`],
      ['配额版本', budget.revision],
    ]} />
    {admin ? <form className="filters" key={budget.revision} onSubmit={save} aria-busy={busy}>
      <label>活跃操作上限<input name="max_active" type="number" step={1} min={1} max={10000}
        defaultValue={budget.max_active} required disabled={busy} /></label>
      <label>受管实例预留上限<input name="max_managed_instances" type="number" step={1} min={0} max={100000}
        defaultValue={budget.max_managed_instances} required disabled={busy} /></label>
      <p className="hint">所设上限用于后续受理，当前操作和预留继续保留。</p>
      <div className="row-actions">
        <button type="submit" className="primary" disabled={busy}>保存额度</button>
        <button type="button" disabled={busy} onClick={reload}>重新读取</button>
      </div>
    </form> : <button type="button" onClick={reload}>刷新额度</button>}
  </>
}
