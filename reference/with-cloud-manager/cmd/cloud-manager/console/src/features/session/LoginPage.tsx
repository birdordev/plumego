import { useState, type FormEvent } from 'react'
import { tenantSchema } from './schema'

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
    <section id="entry" className="entry" hidden={hidden}>
      <form id="login" onSubmit={login} className="login" aria-busy={restoring || busy}>
        <p className="brand"><span className="mark" aria-hidden="true" />云资源管理</p>
        <h1>进入项目控制台</h1>
        <label>租户 ID<input id="tenant" name="tenant" required maxLength={256} autoComplete="organization" /></label>
        <button className="primary" disabled={restoring || busy}>使用企业身份登录</button>
        <p id="login-message" className="notice" role="status" data-error={Boolean(error || notice?.error)}>
          {error || notice?.text}
        </p>
        <noscript>请启用 JavaScript 后登录。</noscript>
      </form>
    </section>
  )
}
