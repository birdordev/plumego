import { useRef } from 'react'
import { DetailForm } from '../../../../shared/detail/DetailForm'
import { formText } from '../../../../shared/detail/form-values'
import { requestRecovery } from './api'
import { evidenceSchema, type Reclaim } from './schema'

type Props = { stamp: number; project: string; reclaim: Reclaim; systemID: string; onSaved: (value: Reclaim) => void }

export function RecoveryRequestForm({ project, reclaim, systemID, stamp, onSaved }: Props) {
  const attempt = useRef<{ evidence: string; id: string } | null>(null)
  async function submit(data: FormData) {
    const parsed = evidenceSchema.safeParse({
      isolation_sha256: formText(data, 'isolation').trim().toLowerCase(),
      evidence_sha256: formText(data, 'evidence').trim().toLowerCase(),
    })
    if (!parsed.success) throw new Error('请为两份证明填写有效的 SHA-256 摘要。')
    const evidence = JSON.stringify(parsed.data)
    if (attempt.current?.evidence !== evidence) attempt.current = { evidence, id: crypto.randomUUID() }
    return requestRecovery(project, reclaim.id, systemID, attempt.current.id, parsed.data)
  }

  return <details>
    <summary>清理服务失联时申请恢复</summary>
    <DetailForm stamp={stamp} submitLabel="登记恢复申请" onSubmit={submit} onSaved={onSaved}>
      <p>请先使旧服务的授权失效，并取得站点隔离和介质清理证明。</p>
      <label>隔离证明 SHA-256<input name="isolation" required maxLength={64} spellCheck={false} autoComplete="off" /></label>
      <label>清理证明 SHA-256<input name="evidence" required maxLength={64} spellCheck={false} autoComplete="off" /></label>
    </DetailForm>
  </details>
}
