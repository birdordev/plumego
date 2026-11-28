import { useState } from 'react'
import { maxRolloutRules, rolloutRule, rolloutRuleKey, rolloutValueLength, type RolloutRule } from './schema'

export function RolloutRuleForm({ rules, disabled, add }: { rules: RolloutRule[]; disabled: boolean; add: (rule: RolloutRule) => void }) {
  const [region, setRegion] = useState('')
  const [action, setAction] = useState('')
  const validated = rolloutRule.safeParse({ region, action })
  const duplicate = validated.success && rules.some((rule) => rolloutRuleKey(rule) === rolloutRuleKey(validated.data))
  const full = rules.length >= maxRolloutRules
  return <form onSubmit={(event) => {
    event.preventDefault()
    if (disabled || full || duplicate || !validated.success) return
    add(validated.data)
    setRegion('')
    setAction('')
  }}>
    <fieldset className="detail-form-fields" disabled={disabled || full}>
      <div className="filters">
        <label>地域<input value={region} onChange={(event) => setRegion(event.target.value)}
          placeholder="例如 ap-guangzhou" maxLength={rolloutValueLength} autoComplete="off" required /></label>
        <label>动作代码<input value={action} onChange={(event) => setAction(event.target.value)}
          placeholder="例如 start" maxLength={rolloutValueLength} autoComplete="off" required /></label>
        <button type="submit" disabled={!validated.success || duplicate}>添加规则</button>
      </div>
    </fieldset>
    {region && action && !validated.success && <p role="status">请输入小写字母、数字或连字符。地域以字母或数字开头，动作以字母开头。</p>}
    {duplicate && <p role="status">地域与动作相同的规则已存在。</p>}
    {full && <p role="status">已达到 {maxRolloutRules} 条规则上限。</p>}
  </form>
}
