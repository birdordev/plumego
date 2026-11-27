import { useState } from 'react'
import { ConsoleIcon } from '../../shared/components/ConsoleIcon'
import { InventoryCoverage } from './InventoryCoverage'
import { OverviewActivity } from './OverviewActivity'
import type { WorkspaceLink } from './navigation'
import { useOverview } from './useOverview'
import './styles.css'

type Props = { base: string; admin: boolean; link: WorkspaceLink; expireSession: () => void }

export default function OverviewPage(props: Props) {
  const [revision, setRevision] = useState(0)
  const reload = () => setRevision((value) => value + 1)
  return <div className="overview">
    <div className="overview-toolbar">
      <a {...props.link('assets')}><ConsoleIcon name="assets" />查看资源清单</a>
      <button className="workspace-refresh" type="button" onClick={reload}>
        <ConsoleIcon name="refresh" />刷新总览
      </button>
    </div>
    <OverviewData key={revision} {...props} onReload={reload} />
  </div>
}

function OverviewData({ base, admin, link, expireSession, onReload }: Props & { onReload: () => void }) {
  const { freshness, operations, alerts } = useOverview(base, admin, expireSession)
  return <>
    <InventoryCoverage result={freshness} link={link} />
    <OverviewActivity operations={operations} alerts={alerts} link={link} onReload={onReload} />
  </>
}
