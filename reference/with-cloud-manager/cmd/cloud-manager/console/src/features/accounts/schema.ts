import { z } from 'zod'
import { revision } from '../../shared/api/fields'
import { pageSchema } from '../../shared/api/page'

export const identifierPattern = String.raw`[A-Za-z0-9][A-Za-z0-9_.\-]{0,127}`
export const credentialPattern = String.raw`[A-Za-z0-9][A-Za-z0-9_.:\/\-]{0,255}`
export const identifier = z.string().regex(new RegExp('^' + identifierPattern + '$'))
export const credentialRef = z.string().regex(new RegExp('^' + credentialPattern + '$'))

export const accountSchema = z.object({
  id: identifier, project: identifier, provider: z.string().min(1), revision,
  mode: z.enum(['pending', 'read_only', 'write_enabled', 'suspended', 'revoked']),
})
export const accountPage = pageSchema(accountSchema)
export const grantPage = pageSchema(z.object({ project: identifier, allow_write: z.boolean() }))

export type Account = z.infer<typeof accountSchema>
export type AccountInput = { id: string; provider: string; credential_ref: string }
export type AccountChange = { action: 'rotate'; credential_ref: string }
  | { action: 'suspend' | 'resume' | 'enable-write' | 'disable-write' | 'enable-bare-metal-power' | 'disable-bare-metal-power' }
  | { action: 'share' | 'unshare' | 'transfer'; target_project: string }
