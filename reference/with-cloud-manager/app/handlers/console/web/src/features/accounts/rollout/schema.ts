import { z } from 'zod'

export const maxRolloutRules = 128
export const rolloutValueLength = 64

export const rolloutRule = z.object({
  region: z.string().regex(/^[a-z0-9][a-z0-9-]{0,63}$/),
  action: z.string().regex(/^[a-z][a-z0-9-]{0,63}$/),
})

export const rolloutPolicy = z.object({
  revision: z.string().regex(/^(0|[1-9][0-9]{0,18})$/),
  rules: z.array(rolloutRule).max(maxRolloutRules),
})

export type RolloutRule = z.infer<typeof rolloutRule>
export type RolloutPolicy = z.infer<typeof rolloutPolicy>

export function rolloutRuleKey({ region, action }: RolloutRule) {
  return region + '/' + action
}
