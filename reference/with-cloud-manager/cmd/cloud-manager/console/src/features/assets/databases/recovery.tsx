import type { z } from 'zod'
import { formText } from '../../../shared/detail/form-values'
import { openDetail } from '../../../shared/detail/store'
import { OperationForm } from '../../operations/forms/OperationForm'
import { projectURL } from '../../session/project'
import type { Asset } from '../types'
import { placementSchema, recoveryPlacement } from './recovery-placement'
import type { databaseRestoreWindowsSchema } from './schema'

type Snapshot = z.infer<typeof databaseRestoreWindowsSchema>
type Placement = NonNullable<ReturnType<typeof recoveryPlacement>>
type Field = Placement['fields'][number]

export function showDatabaseRecovery(asset: Asset, snapshot: Snapshot, placement: Placement) {
  const target = projectURL() + '/databases/recover'
  const schema = placementSchema(placement.fields)
  openDetail('申请恢复到新实例', (stamp) => <>
    <p>{asset.name || asset.id} · {asset.account} / {asset.region}</p>
    <p className="hint">{placement.policy}申请需独立审批。</p>
    <OperationForm stamp={stamp} target={target} revision={asset.ownership_revision} submitLabel="提交恢复申请"
      payload={(data) => {
        const name = formText(data, 'name')
        if (!placement.namePattern.test(name) || name === asset.name) {
          throw new Error(`新名称须以英文字母开头，长度 4–${placement.nameMax}，且与源实例名称不同。`)
        }
        const time = Date.parse(formText(data, 'time') + 'Z')
        if (placement.timeStep === '1' && Number.isFinite(time) && time % 1000 !== 0) {
          throw new Error('此云厂商的恢复时间须精确到秒。')
        }
        if (!snapshot.windows.some((window) => time >= Date.parse(window.start_at) && time <= Date.parse(window.end_at))) {
          throw new Error('恢复时间须位于厂商提供的可恢复时间段。')
        }
        const selected = schema.safeParse(Object.fromEntries(placement.fields.map((field) => {
          const value = formText(data, field.name)
          return [field.name, field.type === 'number' ? Number(value) : value]
        })))
        if (!selected.success) throw new Error('请检查放置参数和实例规格。')
        return { account: asset.account, region: asset.region, source_id: asset.id, observed_at: snapshot.observed_at,
          restore_time: new Date(time).toISOString(), storage_type: placement.storageType, name, ...selected.data }
      }}>
      <label>新实例名称<input name="name" required minLength={4} maxLength={placement.nameMax} /></label>
      <label>恢复时间（UTC）<input name="time" type="datetime-local" required step={placement.timeStep} /></label>
      {placement.fields.map((field) => <PlacementInput key={field.name} field={field} />)}
    </OperationForm>
  </>)
}

function PlacementInput({ field }: { field: Field }) {
  return <label>{field.label}
    {field.type === 'select' ? <select name={field.name} required>
      <option value="">请选择</option>
      {field.options.map((option) => <option key={option}>{option}</option>)}
    </select> : field.type === 'number'
      ? <input name={field.name} type="number" required min={field.min} max={field.max} step={field.step} />
      : <input name={field.name} required maxLength={128} />}
  </label>
}
