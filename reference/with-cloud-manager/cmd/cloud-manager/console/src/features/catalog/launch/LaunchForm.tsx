import { DetailForm } from '../../../shared/detail/DetailForm'
import { formText } from '../../../shared/detail/form-values'
import { showOperation } from '../../operations/detail/open'
import { projectURL } from '../../session/project'
import type { CatalogTemplate } from '../schema'
import { catalogAttempt } from './attempts'
import { sendCatalogLaunch } from './submit'

const namePattern = '[a-zA-Z0-9][a-zA-Z0-9 _.:\\-]{0,79}'

export function LaunchForm({ base, item, stamp }: { base: string; item: CatalogTemplate; stamp: number }) {
  return <DetailForm stamp={stamp} submitLabel="提交实例申请" onSubmit={(data) => {
    const name = formText(data, 'name').trim()
    const body = JSON.stringify({ version: item.version, name })
    return sendCatalogLaunch(catalogAttempt(projectURL(), base + '/launch', body, name))
  }} onSaved={(receipt) => showOperation(receipt.id)}>
    <label>实例名称<input name="name" required maxLength={80} pattern={namePattern} /></label>
  </DetailForm>
}
