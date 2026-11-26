import { kinds } from '../../assets/labels'
import { tagKinds } from './schema'

export type RuleDraft = { id: string; kind: string; keys: string }

type Props = {
  draft: RuleDraft
  index: number
  disabled: boolean
  onChange: (draft: RuleDraft) => void
  onRemove: () => void
}

export function TagPolicyRuleFields({ draft, index, disabled, onChange, onRemove }: Props) {
  return <fieldset disabled={disabled}>
    <legend>规则 {index + 1}</legend>
    <label>资源类型
      <select value={draft.kind} onChange={(event) => onChange({ ...draft, kind: event.target.value })}>
        {tagKinds.options.map((kind) => <option value={kind} key={kind}>{kinds[kind] ?? kind}</option>)}
      </select>
    </label>
    <label>必填标签键，每行一个
      <textarea value={draft.keys} rows={3} required onChange={(event) => onChange({ ...draft, keys: event.target.value })} />
    </label>
    <button type="button" onClick={onRemove}>移除规则</button>
  </fieldset>
}
