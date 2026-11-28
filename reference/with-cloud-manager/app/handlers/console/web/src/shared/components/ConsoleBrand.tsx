import { ConsoleIcon } from './ConsoleIcon'

export function ConsoleBrand() {
  return <p className="brand">
    <span className="mark"><ConsoleIcon name="cloud" /></span>
    <span className="brand-wordmark">云资源管理</span>
  </p>
}
