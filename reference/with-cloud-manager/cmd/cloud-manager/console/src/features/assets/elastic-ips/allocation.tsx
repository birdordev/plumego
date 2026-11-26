import { formText } from '../../../shared/detail/form-values'
import { openDetail } from '../../../shared/detail/store'
import { OperationForm } from '../../operations/forms/OperationForm'
import { projectURL } from '../../session/project'
import { assetsCollection } from '../collection'

const addressNamePattern = '[a-zA-Z0-9][a-zA-Z0-9_.\\-]{0,63}'

export function showElasticIPAllocation() {
  const account = new URLSearchParams(assetsCollection.snapshot().filters).get('account') ?? ''
  const target = projectURL() + '/network-operations'
  openDetail('申请弹性公网 IP', (stamp) => <>
    <p className="hint">申请一个未绑定的 IPv4 地址，按流量后付费，需独立审批。</p>
    <OperationForm stamp={stamp} target={target} submitLabel="提交申请"
      payload={(data) => ({ kind: 'elastic-ip', account: formText(data, 'account').trim(), region: formText(data, 'region').trim(),
        name: formText(data, 'name'), bandwidth_mbps: Number(data.get('bandwidth_mbps')),
        address_pool: formText(data, 'address_pool') || undefined })}>
      <label>账号<input name="account" required maxLength={128} defaultValue={account} /></label>
      <label>地域<input name="region" required maxLength={128} /></label>
      <label>名称<input name="name" required maxLength={64} pattern={addressNamePattern} /></label>
      <label>带宽上限（Mbps）<input name="bandwidth_mbps" type="number" min={5} max={200} step={1} required /></label>
      <label>地址池（仅华为云必填）<input name="address_pool" maxLength={64} pattern={addressNamePattern} /></label>
    </OperationForm>
  </>)
}
