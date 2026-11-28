import { useRef } from 'react'
import { DetailForm } from '../../../../shared/detail/DetailForm'
import { formText } from '../../../../shared/detail/form-values'
import type { Asset } from '../../types'
import { createReclaim } from './api'
import { operationID, type Reclaim } from './schema'

type Props = { project: string; asset: Asset; stamp: number; onSaved: (value: Reclaim) => void }

export function ReclaimRequestForm({ project, asset, stamp, onSaved }: Props) {
  const attempt = useRef<{ sanitizer: string; key: string } | null>(null)

  async function submit(data: FormData) {
    const sanitizer = operationID.safeParse(formText(data, 'sanitizer').trim())
    if (!sanitizer.success) throw new Error('请填写有效的清理服务主体 ID。')
    if (attempt.current?.sanitizer !== sanitizer.data) {
      attempt.current = { sanitizer: sanitizer.data, key: crypto.randomUUID() }
    }
    return createReclaim(project, asset, sanitizer.data, attempt.current.key)
  }

  return <DetailForm stamp={stamp} submitLabel="申请回收" onSubmit={submit} onSaved={onSaved}>
    <p>受理后将锁定设备的电源和 PXE 变更，等待独立审批和站点清理。</p>
    <label>清理服务主体 ID<input name="sanitizer" required maxLength={128} autoComplete="off" /></label>
  </DetailForm>
}
