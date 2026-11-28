import { useState } from 'react'
import { ConsoleIcon } from '../../shared/components/ConsoleIcon'

export function PageLink({ href }: { href: string }) {
  const [result, setResult] = useState<'copied' | 'error'>()
  const label = result === 'copied' ? '链接已复制' : '复制页面链接'

  async function copyLink() {
    try {
      await navigator.clipboard.writeText(href)
      setResult('copied')
    } catch {
      setResult('error')
    }
  }

  return <div className="page-link">
    <button type="button" className="icon-button" aria-label={label} title={label} onClick={() => void copyLink()}>
      <ConsoleIcon name={result === 'copied' ? 'check' : 'copy'} />
    </button>
    <span className={result === 'error' ? 'page-link-error' : 'sr-only'} role="status">
      {result === 'copied' ? '链接已复制。' : result === 'error' ? '复制失败，可从地址栏复制。' : ''}
    </span>
  </div>
}
