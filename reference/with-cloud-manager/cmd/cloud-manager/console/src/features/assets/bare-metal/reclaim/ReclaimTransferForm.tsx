import { DetailForm } from '../../../../shared/detail/DetailForm'
import { formText } from '../../../../shared/detail/form-values'
import type { Project } from '../../../session/store'
import type { Asset } from '../../types'
import { transferReclaimedAsset } from './api'
import type { Reclaim, TransferredOwnership } from './schema'

type Props = {
  project: string; asset: Asset; reclaim: Reclaim; destinations: Project[]; stamp: number
  onSaved: (value: TransferredOwnership) => void
}

export function ReclaimTransferForm({ project, asset, reclaim, destinations, stamp, onSaved }: Props) {
  if (!destinations.length) return <p>当前身份没有可管理的其他项目。</p>
  async function submit(data: FormData) {
    const target = formText(data, 'project')
    if (!destinations.some((destination) => destination.id === target)) throw new Error('请选择有效的目标项目。')
    return transferReclaimedAsset(project, asset, reclaim, target)
  }

  return <DetailForm stamp={stamp} submitLabel="确认转移归属" onSubmit={submit} onSaved={onSaved}>
    <p>转移会消费本次清理凭证。</p>
    <label>目标项目<select name="project" required defaultValue="">
      <option value="">请选择</option>
      {destinations.map((destination) => <option key={destination.id} value={destination.id}>{destination.id}</option>)}
    </select></label>
  </DetailForm>
}
