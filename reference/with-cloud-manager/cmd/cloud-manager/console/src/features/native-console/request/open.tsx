import { useRef } from 'react'
import { DetailForm } from '../../../shared/detail/DetailForm'
import { formText } from '../../../shared/detail/form-values'
import { openDetail } from '../../../shared/detail/store'
import { grantPurposes } from '../labels'
import { projectURL } from '../../session/project'
import { showNativeGrant } from '../access/detail'
import { SerialKeyField } from '../SerialKeyField'
import { requestGrant } from './api'

type InstanceTarget = {
  id: string
  name: string
  account: string
  region: string
  ownership_revision: number
}

export function showNativeRequest(asset: InstanceTarget, serial: boolean) {
  const target = projectURL() + '/console-grants'
  openDetail('申请原生控制台', (stamp) => <GrantRequestForm asset={asset} serial={serial} target={target} stamp={stamp} />)
}

function GrantRequestForm({ asset, serial, target, stamp }:
  { asset: InstanceTarget; serial: boolean; target: string; stamp: number }) {
  const attempts = useRef<Map<string, string>>(undefined)
  return <>
    <p>{asset.name || asset.id}</p>
    <DetailForm stamp={stamp} submitLabel="提交访问申请" onSubmit={(data) => {
      const body = JSON.stringify({ account: asset.account, region: asset.region, asset_id: asset.id,
        purpose: formText(data, 'purpose'), ssh_public_key: serial ? formText(data, 'ssh_public_key').trim() : undefined })
      const keys = attempts.current ??= new Map()
      const key = keys.get(body) ?? crypto.randomUUID()
      keys.set(body, key)
      return requestGrant(target, asset.ownership_revision, key, body)
    }} onSaved={(receipt) => showNativeGrant(receipt.id)}>
      <label>用途<select name="purpose">
        {Object.entries(grantPurposes).map(([value, title]) => <option key={value} value={value}>{title}</option>)}
      </select></label>
      {serial ? <SerialKeyField /> : null}
    </DetailForm>
  </>
}
