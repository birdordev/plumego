import { useState, type FormEvent } from 'react'
import { DetailFields } from '../../../shared/components/DetailFields'
import { setDetailNotice } from '../../../shared/detail/store'
import { requestErrorMessage } from '../../../shared/api/requests'
import { SiteImagePicker } from '../detail/SiteImagePicker'
import { siteIDPattern } from '../schema'
import { saveDraft } from './draft'
import { bootVolume, capacityInput, type Draft, type HostOption, type Image } from './schema'

type Props = {
  sitePath: string; host: HostOption; storageKey: string; stamp: number; signal: AbortSignal
  back: () => void; next: (draft: Draft) => void
}

export default function ProvisioningForm({ sitePath, host, storageKey, stamp, signal, back, next }: Props) {
  const [mode, setMode] = useState<'image' | 'volume'>('image')
  const [image, setImage] = useState<Image>()

  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    const fields = Object.fromEntries(new FormData(event.currentTarget))
    const parsed = capacityInput.safeParse(fields)
    if (!parsed.success || !host.networks.includes(parsed.data.network)) {
      setDetailNotice(stamp, '请检查容量、网络和 IPv4 地址。', true)
      return
    }
    const volume = bootVolume.safeParse(fields.volume)
    let boot: Draft['boot']
    if (mode === 'image' && image && image.image_size_bytes <= parsed.data.disk_mib * (1 << 20)) {
      boot = { kind: 'image', id: image.image_id }
    } else if (mode === 'volume' && volume.success && !host.shared_storage_pool_id) {
      boot = { kind: 'volume', id: volume.data }
    } else {
      setDetailNotice(stamp, mode === 'image' ? '请选择登记镜像，磁盘容量须覆盖镜像字节数。' : '请填写可用的预置启动卷名。', true)
      return
    }
    const draft: Draft = {
      key: crypto.randomUUID(), site: host.site, attempted: false, boot,
      request: { ...parsed.data, required_host: host.id, storage_pool: host.storage_pool,
        shared_storage_pool_id: host.shared_storage_pool_id, ttl_seconds: 300 },
    }
    try {
      saveDraft(storageKey, draft)
      next(draft)
    } catch (error) {
      setDetailNotice(stamp, requestErrorMessage(error), true)
    }
  }

  return <form className="catalog-form" onSubmit={submit}>
    <DetailFields fields={[
      ['站点', host.site], ['宿主机', host.id], ['存储池', host.storage_pool], ['共享池', host.shared_storage_pool_id],
    ]} />
    <label>网络<select name="network" required>{host.networks.map((network) => <option key={network}>{network}</option>)}</select></label>
    <label>vCPU<input name="vcpu" type="number" min="1" max="4096" defaultValue="1" required /></label>
    <label>内存（MiB）<input name="memory_mib" type="number" min="1" max="16777216" defaultValue="1024" required /></label>
    <label>磁盘（MiB）<input name="disk_mib" type="number" min="1" max="1099511627776" defaultValue="10240" required /></label>
    <label>指定 IPv4（可选）<input name="ipv4" maxLength={15} /></label>
    {!host.shared_storage_pool_id && <label>启动来源<select value={mode} onChange={(event) => setMode(event.target.value === 'volume' ? 'volume' : 'image')}>
      <option value="image">已登记镜像</option><option value="volume">专用预置启动卷</option>
    </select></label>}
    {mode === 'image' ? <SiteImagePicker sitePath={sitePath} signal={signal} chosen={image} choose={setImage} /> :
      <label>预置启动卷<input name="volume" pattern={siteIDPattern} maxLength={64} required /></label>}
    <div className="row-actions">
      <button type="submit" className="primary">核对预留配置</button>
      <button type="button" onClick={back}>更换宿主机</button>
    </div>
  </form>
}
