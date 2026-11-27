import { ConsoleTable } from '../../../shared/components/ConsoleTable'
import { timestamp } from '../../../shared/display/time'
import type { FenceHistory } from './schema'
import { labels } from './state'

export function FenceEvents({ events }: Pick<FenceHistory, 'events'>) {
  return <section aria-label="隔离事件">
    <h3>最近的隔离事件</h3>
    {events.length === 0 ? <p>暂无隔离事件。</p> : <ConsoleTable
      columns={['操作', '执行人', '宿主机版本', '代次', '时间', '证据 SHA-256', '替换代理']}
      rows={events.map((event) => ({ key: String(event.revision), cells: [
        labels[event.action], event.actor, event.revision, event.generation, timestamp(event.occurred_at),
        event.evidence_sha256, event.replacement_agent,
      ] }))} />}
  </section>
}
