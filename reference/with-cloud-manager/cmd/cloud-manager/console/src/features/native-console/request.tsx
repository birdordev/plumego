import { useRef } from 'react'
import { DetailForm } from '../../shared/detail/DetailForm'
import { formText } from '../../shared/detail/form-values'
import { openDetail } from '../../shared/detail/store'
import { grantPurposes } from '../../shared/display/labels'
import type { Asset } from '../assets/types'
import { request } from '../session/client'
import { projectURL } from '../session/project'
import { showNativeGrant } from './grant'
import { grantSchema } from './schema'
import { SerialKeyField } from './SerialKeyField'

export function showNativeRequest(asset: Asset, serial: boolean) {
  const target = projectURL() + '/console-grants'
  openDetail('申请原生控制台', (stamp) => <GrantRequestForm asset={asset} serial={serial} target={target} stamp={stamp} />)
}

function GrantRequestForm({ asset, serial, target, stamp }:
  { asset: Asset; serial: boolean; target: string; stamp: number }) {
  const attempts = useRef<Map<string, string>>(undefined)
  return <>
    <p>{asset.name || asset.id}</p>
    <DetailForm stamp={stamp} submitLabel="提交访问申请" onSubmit={(data) => {
      const body = JSON.stringify({ account: asset.account, region: asset.region, asset_id: asset.id,
        purpose: formText(data, 'purpose'), ssh_public_key: serial ? formText(data, 'ssh_public_key').trim() : undefined })
      const keys = attempts.current ??= new Map()
      const key = keys.get(body) ?? crypto.randomUUID()
      keys.set(body, key)
      return request(target, { method: 'POST', key, match: asset.ownership_revision, body }, grantSchema)
    }} onSaved={(receipt) => showNativeGrant(receipt.id)}>
      <label>用途<select name="purpose">
        {Object.entries(grantPurposes).map(([value, title]) => <option key={value} value={value}>{title}</option>)}
      </select></label>
      {serial ? <SerialKeyField /> : null}
    </DetailForm>
  </>
}
