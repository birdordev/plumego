import { useCallback, useState, type FormEvent } from 'react'
import { requestErrorMessage } from '../../../shared/api/requests'
import { DetailFields } from '../../../shared/components/DetailFields'
import { setDetailNotice } from '../../../shared/detail/store'
import { PagedResults } from '../detail/PagedResults'
import { SiteImagePicker } from '../detail/SiteImagePicker'
import { listHosts } from '../provisioning/api'
import type { HostOption, Image } from '../provisioning/schema'
import { evidenceSchema } from '../schema'
import { saveAttempt } from './attempt'
import type { Attempt, Eligibility } from './schema'

type Source = Exclude<Eligibility, { mode: 'unavailable' }>
type Props = {
  base: string; site: string; sitePath: string; source: Source; keyName: string; stamp: number; signal: AbortSignal
  back: () => void; prepare: (attempt: Attempt) => void
}

export default function MigrationForm({ base, site, sitePath, source, keyName, stamp, signal, back, prepare }: Props) {
  const { vm } = source
  const [automatic, setAutomatic] = useState(source.mode === 'evacuation')
  const [host, setHost] = useState<HostOption>()
  const [image, setImage] = useState<Image>()
  const read = useCallback((path: string, after: string, requestSignal: AbortSignal) => listHosts(path, after, requestSignal, site), [site])

  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    const fields = new FormData(event.currentTarget)
    const proof = evidenceSchema.safeParse(fields.get('evidence'))
    if (!proof.success || !image || (!automatic && !host) || fields.get('confirmed') !== 'yes') {
      setDetailNotice(stamp, '请选择目标、核对备份并填写现场证据摘要。', true)
      return
    }
    if (image.image_size_bytes > vm.capacity.disk_mib * (1 << 20)) {
      setDetailNotice(stamp, '备份字节数超过虚拟机原磁盘容量。', true)
      return
    }
    const attempt: Attempt = {
      id: crypto.randomUUID(), source, host: automatic ? undefined : host,
      image: image.image_id, evidence: proof.data, verified: fields.get('verified') === 'yes', started: false,
    }
    try {
      saveAttempt(keyName, attempt)
      prepare(attempt)
    } catch (error) {
      setDetailNotice(stamp, requestErrorMessage(error), true)
    }
  }

  return <form className="catalog-form" onSubmit={submit}>
    <DetailFields fields={[
      ['虚拟机', vm.id], ['源宿主机', vm.host], ['源放置 ID', vm.placement_id], ['网络', vm.network], ['原 IPv4', vm.ipv4],
      ['隔离代次', source.mode === 'evacuation' ? source.source_fence_generation : undefined],
    ]} />
    {source.mode === 'evacuation' && <label>疏散选址<select value={automatic ? 'auto' : 'manual'}
      onChange={(event) => setAutomatic(event.target.value === 'auto')}>
      <option value="auto">在其他故障域自动选址</option><option value="manual">指定目标宿主机</option>
    </select></label>}
    {!automatic && <section aria-label="目标宿主机">
      <h3>目标宿主机</h3>
      {host ? <><p>{host.id} · {host.shared_storage_pool_id ?? host.storage_pool}</p>
        <button type="button" onClick={() => setHost(undefined)}>更换目标</button></>
        : <PagedResults target={base} read={read} signal={signal}>{(items) => {
          const candidates = items.filter((candidate) => candidate.id !== vm.host && candidate.networks.includes(vm.network))
          return candidates.length ? <div className="row-actions">{candidates.map((candidate) =>
            <button type="button" key={candidate.id} onClick={() => setHost(candidate)}>{candidate.id}</button>)}</div>
            : <p role="status">本页暂无同网络候选宿主机。</p>
        }}</PagedResults>}
    </section>}
    <SiteImagePicker sitePath={sitePath} signal={signal} chosen={image} choose={setImage} />
    <label>现场证据 SHA-256<input name="evidence" required maxLength={64} pattern="[a-f0-9]{64}" /></label>
    <label><input name="verified" value="yes" type="checkbox" /> 要求目标拥有当前有效的备份校验回执</label>
    <label><input name="confirmed" value="yes" type="checkbox" required /> 我已核对源状态、备份和可恢复时间点</label>
    <div className="row-actions">
      <button type="submit" className="primary">核对迁移申请</button>
      <button type="button" onClick={back}>返回迁移记录</button>
    </div>
  </form>
}
