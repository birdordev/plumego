import { useState } from 'react'
import { DetailFields } from '../../../shared/components/DetailFields'
import { SiteImagePicker } from '../detail/SiteImagePicker'
import { vmStatus } from '../labels'
import type { Image } from '../provisioning/schema'
import { evidenceSchema } from '../schema'
import { member, type Candidate, type Member } from './schema'

export function MemberEditor({ candidate, sitePath, signal, onAdd, back }: {
  candidate: Candidate; sitePath: string; signal: AbortSignal; onAdd: (item: Member) => void; back: () => void
}) {
  const [image, setImage] = useState<Image>()
  const [evidence, setEvidence] = useState('')
  const [verified, setVerified] = useState(false)
  const needsSpace = image && Math.ceil(image.image_size_bytes / 1_048_576) > candidate.disk_mib
  return <form className="catalog-form" onSubmit={(event) => {
    event.preventDefault()
    if (!image || needsSpace) return
    const parsed = member.safeParse({ vm_id: candidate.vm_id, vm_revision: candidate.vm_revision, image_id: image.image_id,
      evidence_sha256: evidence.trim().toLowerCase(), require_verified_backup: verified })
    if (parsed.success) onAdd(parsed.data)
  }}>
    <h3>填写虚拟机疏散证据</h3>
    <DetailFields fields={[
      ['虚拟机', candidate.vm_id], ['版本', candidate.vm_revision], ['状态', vmStatus(candidate.state)],
      ['网络', candidate.network], ['原 IPv4', candidate.ipv4], ['预留磁盘（MiB）', candidate.disk_mib],
    ]} />
    <SiteImagePicker sitePath={sitePath} signal={signal} chosen={image} choose={setImage} />
    {needsSpace && <p role="alert">镜像超过该虚拟机的磁盘预算。</p>}
    <label>现场证据 SHA-256
      <input value={evidence} onChange={(event) => setEvidence(event.target.value)} autoComplete="off"
        maxLength={64} pattern="[0-9A-Fa-f]{64}" required />
    </label>
    <label><input type="checkbox" checked={verified} onChange={(event) => setVerified(event.target.checked)} /> 要求有效备份回执</label>
    <div className="row-actions">
      <button className="primary" disabled={!image || needsSpace || !evidenceSchema.safeParse(evidence.toLowerCase()).success}>加入清单</button>
      <button type="button" onClick={back}>返回候选清单</button>
    </div>
  </form>
}
