import { useRef, useState, type FormEvent } from 'react'
import { DetailFields } from '../../../shared/components/DetailFields'
import { setDetailNotice } from '../../../shared/detail/store'
import { useDetailAction } from '../../../shared/detail/useDetailAction'
import { evidenceSchema, siteID, siteIDPattern } from '../schema'
import { readBackup, requestCleanup } from './api'
import type { BackupImage, Cleanup, RetainedVolume } from './schema'

type Props = {
  target: string; sitePath: string; volume: RetainedVolume; stamp: number; signal: AbortSignal
  back: () => void; onSaved: (job: Cleanup) => void
}

export default function CleanupRequest({ target, sitePath, volume, stamp, signal, back, onSaved }: Props) {
  const [backup, setBackup] = useState<BackupImage>()
  const attempt = useRef<{ input: string; id: string }>(undefined)
  const { busy, run } = useDetailAction(stamp)

  function lookup(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    const id = siteID.safeParse(new FormData(event.currentTarget).get('image'))
    if (!id.success) return
    void run(async () => {
      const image = await readBackup(sitePath, id.data, signal)
      if (image.image_id !== id.data || image.image_size_bytes > volume.max_backup_bytes ||
          (volume.expected_backup_bytes && image.image_size_bytes !== volume.expected_backup_bytes)) {
        throw new Error('登记备份的字节数或标识与源卷要求不符。')
      }
      return image
    }, setBackup)
  }

  if (!backup) return <form className="catalog-form" onSubmit={lookup}>
    <fieldset className="detail-form-fields" disabled={busy}>
      <label>已登记备份 ID<input name="image" required maxLength={64} pattern={siteIDPattern} /></label>
      <div className="row-actions">
        <button type="submit" className="primary">核对登记备份</button>
        <button type="button" onClick={back}>返回清理历史</button>
      </div>
    </fieldset>
  </form>

  function submit(event: FormEvent<HTMLFormElement>, image: BackupImage) {
    event.preventDefault()
    const fields = new FormData(event.currentTarget)
    const proof = evidenceSchema.safeParse(fields.get('proof'))
    if (!proof.success || fields.get('verified') !== 'yes') {
      setDetailNotice(stamp, '请核对备份和保留政策，并填写证据摘要。', true)
      return
    }
    const input = JSON.stringify([image.image_id, proof.data])
    if (attempt.current?.input !== input) attempt.current = { input, id: crypto.randomUUID() }
    const { id } = attempt.current
    void run(() => requestCleanup(target, volume.host_revision, id, image.image_id, proof.data, signal), onSaved)
  }

  return <form className="catalog-form" onSubmit={(event) => submit(event, backup)}>
    <DetailFields fields={[
      ['放置 ID', volume.placement_id], ['原宿主机', volume.host], ['宿主机版本', volume.host_revision],
      ['备份 ID', backup.image_id], ['备份字节数', backup.image_size_bytes], ['备份 SHA-256', backup.image_sha256],
    ]} />
    <fieldset className="detail-form-fields" disabled={busy}>
      <label>保留策略证据 SHA-256<input name="proof" required maxLength={64} pattern="[a-f0-9]{64}" /></label>
      <label><input name="verified" value="yes" type="checkbox" required /> 我已核对独立备份和站点保留策略</label>
      <div className="row-actions">
        <button type="submit" className="primary">确认申请清理</button>
        <button type="button" onClick={() => { setDetailNotice(stamp); setBackup(undefined) }}>更换备份</button>
      </div>
    </fieldset>
  </form>
}
