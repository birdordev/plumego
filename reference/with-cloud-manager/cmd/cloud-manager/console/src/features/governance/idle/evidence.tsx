import { DetailFields } from '../../../shared/components/DetailFields'
import { openDetail } from '../../../shared/detail/store'
import { timestamp } from '../../../shared/display/labels'
import type { IdleInstance } from './schema'

const number = new Intl.NumberFormat('zh-CN', { maximumFractionDigits: 2 })

export function formatNetwork(rate: number | undefined) {
  return rate === undefined ? '—' : number.format(rate / 1024) + ' KiB/s'
}

export function formatCPU(load: number | undefined) {
  return load === undefined ? '—' : number.format(load) + '%'
}

export function showIdleEvidence(row: IdleInstance) {
  const data = row.evidence
  if (!data) return
  openDetail('实例监控证据', () => <DetailFields fields={[
    ['实例', row.id], ['厂商', row.provider], ['开始时间', timestamp(data.from)], ['截止时间', timestamp(data.until)],
    ['采样间隔', data.period_seconds + ' 秒'],
    ['CPU 样本', data.cpu_samples + ' / ' + data.expected_samples],
    ['网络样本', data.network_samples + ' / ' + data.expected_samples],
    ['CPU 峰值', formatCPU(data.cpu_peak_percent)],
    ['网络峰值', formatNetwork(data.network_peak_bytes_per_second)],
    ['证据摘要', data.evidence_sha256],
  ]} />)
}
