import { z } from 'zod'
import { formText } from '../../../shared/detail/form-values'
import { openDetail } from '../../../shared/detail/store'
import { OperationForm } from '../../operations/forms/OperationForm'
import { projectURL } from '../../session/project'
import type { Asset } from '../types'

const nameSchema = z.string().regex(/^[A-Za-z][A-Za-z0-9_-]{3,59}$/)

export function showCacheRename(asset: Asset) {
  const target = projectURL() + '/caches/rename'
  openDetail('申请缓存实例改名', (stamp) => <>
    <p>{asset.account} / {asset.region} · {asset.id} · 归属版本 {asset.ownership_revision}</p>
    <p>当前名称 {asset.name || '未命名'} · 需独立审批</p>
    <OperationForm stamp={stamp} target={target} revision={asset.ownership_revision} submitLabel="提交改名申请"
      payload={(data) => {
        const name = formText(data, 'name')
        if (!nameSchema.safeParse(name).success) throw new Error('新名称须为 4–60 位，以英文字母开头，可包含字母、数字、下划线和连字符。')
        if (name === asset.name) throw new Error('请填写不同于当前名称的新名称。')
        return { account: asset.account, region: asset.region, asset_id: asset.id, current_name: asset.name, name }
      }}>
      <label>新名称<input name="name" required minLength={4} maxLength={60} /></label>
      <p className="hint">以英文字母开头，可包含字母、数字、下划线和连字符。</p>
    </OperationForm>
  </>)
}
