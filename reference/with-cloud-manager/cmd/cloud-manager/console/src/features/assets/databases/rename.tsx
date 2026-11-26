import { z } from 'zod'
import { formText } from '../../../shared/detail/form-values'
import { openDetail } from '../../../shared/detail/store'
import { OperationForm } from '../../operations/forms/OperationForm'
import { projectURL } from '../../session/project'
import type { Asset } from '../types'

const namePattern = '[A-Za-z\\p{Script=Han}][A-Za-z0-9_\\p{Script=Han}]{3,58}'
const nameSchema = z.string().regex(new RegExp('^(?:' + namePattern + ')$', 'u'))
  .refine((name) => new TextEncoder().encode(name).length <= 64)

export function showDatabaseRename(asset: Asset) {
  const target = projectURL() + '/databases/rename'
  openDetail('申请数据库实例改名', (stamp) => <>
    <p>{asset.account} / {asset.region} · {asset.id} · 归属版本 {asset.ownership_revision}</p>
    <p>当前名称 {asset.name || '未命名'} · 需独立审批</p>
    <OperationForm stamp={stamp} target={target} revision={asset.ownership_revision} submitLabel="提交改名申请"
      payload={(data) => {
        const name = formText(data, 'name')
        if (!nameSchema.safeParse(name).success) throw new Error('名称须为 4–59 个字符，且不超过 64 字节。')
        if (name === asset.name) throw new Error('请填写不同于当前名称的新名称。')
        return { account: asset.account, region: asset.region, asset_id: asset.id, current_name: asset.name, name }
      }}>
      <label>新名称<input name="name" required pattern={namePattern} /></label>
      <p className="hint">以英文字母或汉字开头，可包含数字和下划线，最多 64 字节。</p>
    </OperationForm>
  </>)
}
