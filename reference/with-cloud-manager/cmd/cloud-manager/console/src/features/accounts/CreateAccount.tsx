import { useState } from 'react'
import { useDetailAction } from '../../shared/detail/useDetailAction'
import { providerOptions } from '../../shared/display/providers'
import { registerAccount } from './api'
import { credentialPattern, credentialRef, identifier, identifierPattern, type Account } from './schema'

export default function CreateAccount({ target, stamp, signal, onSaved }: {
  target: string; stamp: number; signal: AbortSignal; onSaved: (account: Account) => void
}) {
  const [id, setID] = useState('')
  const [provider, setProvider] = useState<string>(providerOptions[0][0])
  const [ref, setRef] = useState('')
  const { busy, run } = useDetailAction(stamp)
  const valid = identifier.safeParse(id).success && credentialRef.safeParse(ref).success
  return <form className="catalog-form" onSubmit={(event) => {
    event.preventDefault()
    if (valid) void run(() => registerAccount(target, { id, provider, credential_ref: ref }, signal), onSaved)
  }}>
    <label>账号 ID<input value={id} required pattern={identifierPattern} maxLength={128}
      disabled={busy} onChange={(event) => setID(event.target.value)} /></label>
    <label>云厂商<select value={provider} disabled={busy} onChange={(event) => setProvider(event.target.value)}>
      {providerOptions.map(([key, name]) => <option key={key} value={key}>{name}</option>)}
    </select></label>
    <label>凭据引用<input type="password" autoComplete="off" value={ref} required pattern={credentialPattern} maxLength={256}
      disabled={busy} onChange={(event) => setRef(event.target.value)} /></label>
    <p>填写管理端已配置的凭据引用。账号登记后进入待身份校验。</p>
    <div className="row-actions"><button className="primary" disabled={!valid || busy}>确认登记</button></div>
  </form>
}
