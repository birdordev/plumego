import { DetailForm } from '../../../../shared/detail/DetailForm'
import { formText } from '../../../../shared/detail/form-values'
import { decisionSchema, type Decision, type Reclaim } from './schema'

type Props = { stamp: number; label: string; canApprove: boolean; submit: (decision: Decision) => Promise<Reclaim>; onSaved: (value: Reclaim) => void }

export function ReclaimDecisionForm({ stamp, label, canApprove, submit, onSaved }: Props) {
  async function decide(data: FormData) {
    const parsed = decisionSchema.safeParse(formText(data, 'decision'))
    if (!parsed.success || (!canApprove && parsed.data === 'approve')) throw new Error('请选择有效的审批决定。')
    return submit(parsed.data)
  }

  return <DetailForm stamp={stamp} submitLabel={label} onSubmit={decide} onSaved={onSaved}>
    <label>审批决定<select name="decision" required defaultValue="">
      <option value="">请选择</option>
      {canApprove ? <option value="approve">批准</option> : null}
      <option value="reject">拒绝</option>
    </select></label>
  </DetailForm>
}
