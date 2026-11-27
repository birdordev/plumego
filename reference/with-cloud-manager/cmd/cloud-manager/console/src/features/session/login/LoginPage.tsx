import { useState, type FormEvent } from 'react'
import { ConsoleBrand } from '../../../shared/components/ConsoleBrand'
import { tenantSchema } from '../schema'
import './styles.css'

type Props = {
  hidden: boolean
  restoring: boolean
  notice?: { text: string; error?: boolean }
  onLogin: (tenant: string) => Promise<void>
}

export function LoginPage({ hidden, restoring, notice, onLogin }: Props) {
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')

  async function login(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (busy || restoring) return
    const tenant = tenantSchema.safeParse(new FormData(event.currentTarget).get('tenant'))
    if (!tenant.success) {
      setError('请填写有效的租户 ID。')
      return
    }
    setError('')
    setBusy(true)
    try {
      await onLogin(tenant.data)
    } catch (failure) {
      setError(failure instanceof Error ? failure.message : '登录失败。')
    } finally {
      setBusy(false)
    }
  }

  return (
    <main id="entry" className="entry" hidden={hidden}>
      <form id="login" onSubmit={login} className="login" aria-labelledby="login-title" aria-busy={restoring || busy}>
        <div className="login-heading">
          <ConsoleBrand />
          <h1 id="login-title">进入项目控制台</h1>
        </div>
        <label>租户 ID<input id="tenant" name="tenant" required maxLength={256} autoComplete="organization"
          disabled={restoring || busy} aria-describedby="login-message" /></label>
        <button className="primary" disabled={restoring || busy}>
          {restoring ? '正在检查会话…' : busy ? '正在登录…' : '使用企业身份登录'}
        </button>
        <p id="login-message" className="notice" role="status" data-error={Boolean(error || notice?.error)}>
          {error || notice?.text}
        </p>
        <noscript>请启用 JavaScript 后登录。</noscript>
      </form>
    </main>
  )
}
