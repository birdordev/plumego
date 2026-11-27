import { useCallback, useState } from 'react'
import { requestErrorMessage } from '../../../shared/api/requests'
import { setDetailNotice } from '../../../shared/detail/store'
import { PagedResults } from '../../../shared/collection/PagedResults'
import { vmStatus } from '../labels'
import { listCandidates } from './api'
import { saveDraft } from './draft'
import { MemberEditor } from './MemberEditor'
import { MemberFields } from './MemberFields'
import { maxMembers, type Candidate, type Draft, type Member, type Source } from './schema'

export default function EvacuationEditor({ target, sitePath, source, keyName, signal, stamp, back, review }: {
  target: string; sitePath: string; source: Source; keyName: string; signal: AbortSignal; stamp: number
  back: () => void; review: (draft: Draft) => void
}) {
  const [members, setMembers] = useState<Member[]>([])
  const [editing, setEditing] = useState<Candidate>()
  const [valid, setValid] = useState(true)
  const read = useCallback(async (path: string, after: string, requestSignal: AbortSignal) => {
    const page = await listCandidates(path, after, requestSignal)
    if (page.mode !== source.mode || page.host_revision !== source.host_revision ||
      page.source_fence_generation !== source.source_fence_generation) {
      if (!requestSignal.aborted) setValid(false)
      throw new Error('宿主机隔离状态或版本已变化。请返回疏散工作台重新选择。')
    }
    return page
  }, [source])

  function preview() {
    try {
      const draft: Draft = { ...source, id: crypto.randomUUID(), items: members, phase: 'ready' }
      saveDraft(keyName, draft)
      review(draft)
    } catch (error) {
      setDetailNotice(stamp, requestErrorMessage(error), true)
    }
  }

  return <>
    <p>源宿主机版本 {source.host_revision} · 隔离代次 {source.source_fence_generation} · 已选 {members.length}/{maxMembers} 台</p>
    <section aria-label="已选疏散成员">
      {members.map((item, index) => <section key={item.vm_id}>
        <h3>成员 {index + 1}</h3>
        <MemberFields member={item} />
        {!editing && <button type="button" onClick={() => setMembers((current) => current.filter(({ vm_id }) => vm_id !== item.vm_id))}
          aria-label={'移除 ' + item.vm_id}>移除</button>}
      </section>)}
    </section>
    <div hidden={editing !== undefined}>
      <h3>候选虚拟机</h3>
      <PagedResults target={target} signal={signal} read={read}>{(items) => items.length
        ? <ul className="evacuation-list">{items.map((item) => <li key={item.vm_id}>
          <span>{item.vm_id} · {vmStatus(item.state)} · {item.network}</span>
          <button type="button" disabled={!valid || members.length >= maxMembers || members.some(({ vm_id }) => vm_id === item.vm_id)}
            onClick={() => setEditing(item)} aria-label={'选择 ' + item.vm_id}>选择</button>
        </li>)}</ul> : <p>当前没有可选的虚拟机。</p>}</PagedResults>
    </div>
    {editing ? <MemberEditor key={editing.vm_id} candidate={editing} sitePath={sitePath} signal={signal} back={() => setEditing(undefined)}
      onAdd={(item) => {
        setMembers((current) => [...current, item])
        setEditing(undefined)
      }} />
      : <div className="row-actions">
        <button type="button" className="primary" disabled={!valid || members.length === 0} onClick={preview}>核对疏散清单</button>
        <button type="button" onClick={back}>返回疏散工作台</button>
      </div>}
  </>
}
