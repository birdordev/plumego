import { requestErrorMessage } from '../../shared/api/requests'
import { ConsoleIcon, type ConsoleIconName } from '../../shared/components/ConsoleIcon'
import { RowAction, StateBadge } from '../../shared/components/RowAction'
import { timestamp } from '../../shared/display/time'
import type { WorkspaceLink } from './navigation'

export type Activity = {
  id: string
  title: string
  subject: string
  status: string
  at: string
}

type Props = {
  id: string
  title: string
  icon: ConsoleIconName
  empty: string
  pending: boolean
  error?: unknown
  items: readonly Activity[]
  target: ReturnType<WorkspaceLink>
  showRecord: (id: string) => void
}

export function ActivityPanel({ id, title, icon, empty, pending, error, items, target, showRecord }: Props) {
  return <section className="panel overview-panel" aria-labelledby={id}>
    <div className="overview-heading">
      <h2 id={id}><ConsoleIcon name={icon} />{title}</h2>
      <a {...target}>查看全部<span className="sr-only">{title}</span></a>
    </div>
    {pending && <p className="hint overview-placeholder" role="status">正在读取…</p>}
    {error !== undefined && <p className="overview-error" role="alert">{requestErrorMessage(error)}</p>}
    {!pending && error === undefined && !items.length && <p className="hint overview-placeholder">{empty}</p>}
    {items.length > 0 && <ul className="overview-activity">
      {items.map((item) => <li key={item.id}>
        <div className="overview-record">
          <RowAction onClick={() => showRecord(item.id)}>{item.title}<span className="sr-only"> {item.id}</span></RowAction>
          <span className="minor">{item.subject}</span>
        </div>
        <div className="overview-record-state">
          <StateBadge status={item.status} />
          <time className="minor" dateTime={item.at}>{timestamp(item.at)}</time>
        </div>
      </li>)}
    </ul>}
  </section>
}
