import { z } from 'zod'
import { formText } from '../../../shared/detail/form-values'
import { openDetail } from '../../../shared/detail/store'
import { OperationForm } from '../../operations/forms/OperationForm'
import { projectURL } from '../../session/project'
import type { Asset } from '../types'
import type { databaseRestoreWindowsSchema } from './schema'

type Snapshot = z.infer<typeof databaseRestoreWindowsSchema>
const uuid = z.string().regex(/^[a-f0-9]{8}(?:-[a-f0-9]{4}){3}-[a-f0-9]{12}$/)
const code = z.string().regex(/^[A-Za-z0-9][A-Za-z0-9_.-]{0,95}$/)
const huaweiPlacement = z.object({ vpc_id: uuid, subnet_id: uuid, security_group_id: uuid, zone: code, flavor: code,
  storage_gib: z.number().int().min(20).max(10000).multipleOf(10) })
const alibabaPlacement = z.object({ vpc_id: z.string().regex(/^vpc-[a-z0-9]+$/), subnet_id: z.string().regex(/^vsw-[a-z0-9]+$/),
  zone: code, flavor: code, storage_gib: z.number().int().min(20).max(64000).multipleOf(5) })

const commonFields = [
  { name: 'vpc_id', label: '目标 VPC ID' },
  { name: 'subnet_id', label: '子网的网络 ID' },
  { name: 'zone', label: '可用区' },
  { name: 'flavor', label: '数据库规格' },
] as const

export function showDatabaseRecovery(asset: Asset, snapshot: Snapshot) {
  const target = projectURL() + '/databases/recover'
  const alibaba = asset.provider === 'alibaba-ecs'
  const fields = alibaba ? commonFields : [...commonFields, { name: 'security_group_id', label: '安全组 ID' }]
  const storageType = alibaba ? 'cloud_essd' : 'CLOUDSSD'
  openDetail('申请恢复到新实例', (stamp) => <>
    <p>{asset.name || asset.id} · {asset.account} / {asset.region}</p>
    <p className="hint">{alibaba ? '克隆阿里云基础版 MySQL ESSD 实例。恢复后使用指定 VPC，并核对继承的白名单和加密配置。'
      : '恢复到运维配置中的独立 VPC，创建按需计费的 MySQL 单机实例。'}申请需独立审批。</p>
    <OperationForm stamp={stamp} target={target} revision={asset.ownership_revision} submitLabel="提交恢复申请"
      payload={(data) => recoveryPayload(data, asset, snapshot, alibaba, storageType)}>
      <label>新实例名称<input name="name" required minLength={4} maxLength={64} /></label>
      <label>恢复时间（UTC）<input name="time" type="datetime-local" required step={alibaba ? '1' : '0.001'} /></label>
      {fields.map((field) => <label key={field.name}>
        {alibaba && field.name === 'subnet_id' ? '目标 vSwitch ID' : field.label}
        <input name={field.name} required maxLength={96} />
      </label>)}
      <label>{storageType} 容量（GiB）<input name="storage_gib" type="number" required min={20} max={alibaba ? 64000 : 10000} step={alibaba ? 5 : 10} /></label>
    </OperationForm>
  </>)
}

function recoveryPayload(data: FormData, asset: Asset, snapshot: Snapshot, alibaba: boolean, storageType: string) {
  const name = formText(data, 'name')
  const namePattern = alibaba ? /^[A-Za-z][A-Za-z0-9_-]{3,63}$/ : /^[A-Za-z][A-Za-z0-9_.-]{3,63}$/
  if (!namePattern.test(name) || name === asset.name) {
    throw new Error(alibaba ? '新名称须以英文字母开头，使用字母、数字、下划线或短横线，长度 4–64，并与源实例名称不同。'
      : '新名称须以英文字母开头，长度 4–64，且与源实例名称不同。')
  }
  const time = Date.parse(formText(data, 'time') + 'Z')
  if (alibaba && Number.isFinite(time) && time % 1000 !== 0) {
    throw new Error('阿里云的恢复时间须精确到秒。')
  }
  if (!snapshot.windows.some((window) => time >= Date.parse(window.start_at) && time <= Date.parse(window.end_at))) {
    throw new Error('恢复时间须位于厂商提供的可恢复时间段。')
  }
  const selected = (alibaba ? alibabaPlacement : huaweiPlacement).safeParse({
    ...Object.fromEntries(commonFields.map(({ name }) => [name, formText(data, name)])),
    security_group_id: formText(data, 'security_group_id'),
    storage_gib: Number(formText(data, 'storage_gib')),
  })
  if (!selected.success) throw new Error('请检查放置参数和磁盘容量。')
  return { account: asset.account, region: asset.region, source_id: asset.id, observed_at: snapshot.observed_at,
    restore_time: new Date(time).toISOString(), storage_type: storageType, name, ...selected.data }
}
